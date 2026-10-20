package syncz

// Mídia recebida (task 142 do sync-zap): o gateway baixa a mídia de uma mensagem recebida e avisa com media.ready (ou
// media.failed). O evento traz o wa_message_id da mensagem de origem, para casar com o message.received, e uma URL de
// capacidade relativa (/media/d/<token>): de uso único, com vencimento, e ela mesma é a credencial. BaixarMidia baixa
// por ela em streaming; quem chama fecha o corpo e põe o próprio teto de tamanho.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Tipos de evento de mídia recebida (espelham internal/modulos/core/event_types.go).
const (
	TipoMediaReady  = "media.ready"
	TipoMediaFailed = "media.failed"
)

// PayloadMediaReady é o corpo de media.ready.
type PayloadMediaReady struct {
	MediaID      string `json:"media_id"`
	WAMensagemID string `json:"wa_message_id,omitempty"`
	URL          string `json:"url"`
	MimeType     string `json:"mimetype"`
	Tamanho      int64  `json:"size"`
	// ExpiraEm: depois dele (ou do primeiro download) a URL devolve ErrNaoEncontrado.
	ExpiraEm time.Time `json:"expires_at"`
}

// PayloadMediaFailed é o corpo de media.failed (Motivo: "too_large" quando a mídia passa do teto do gateway).
type PayloadMediaFailed struct {
	MediaID      string `json:"media_id"`
	WAMensagemID string `json:"wa_message_id,omitempty"`
	Motivo       string `json:"reason"`
}

// BaixarMidia (REST) baixa pela URL de capacidade do media.ready: relativa ao BaseURL ou absoluta no mesmo host. Não
// manda a chave de API (a URL é a credencial). Status de erro viram os sentinelas de sempre (404 = ErrNaoEncontrado:
// vencida, já usada ou inexistente; 429 = ErrIndisponivel).
func (c *clienteREST) BaixarMidia(ctx context.Context, urlMidia string) (io.ReadCloser, error) {
	u, err := c.urlDaMidia(urlMidia)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("syncz: criar requisicao http: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("syncz: erro de rede: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		corpo, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, erroPorStatusHTTP(resp.StatusCode, corpo)
	}
	return resp.Body, nil
}

// urlDaMidia resolve a URL do evento contra o BaseURL; URL absoluta de outro host é recusada (ErrInvalido), para a
// mídia nunca ser buscada fora do gateway configurado.
func (c *clienteREST) urlDaMidia(urlMidia string) (string, error) {
	if strings.HasPrefix(urlMidia, "/") && !strings.HasPrefix(urlMidia, "//") {
		return c.baseURL + urlMidia, nil
	}
	alvo, err := url.Parse(urlMidia)
	base, errBase := url.Parse(c.baseURL)
	if err != nil || errBase != nil || alvo.Scheme != base.Scheme || alvo.Host != base.Host {
		return "", novoErroAPI(ErrInvalido, "url de midia fora do gateway configurado")
	}
	return urlMidia, nil
}

// BaixarMidia (gRPC): o proto não tem RPC de mídia e a URL de capacidade é do edge HTTP, que o cliente gRPC não
// conhece. Mesmo critério de StatusMensagem: ErrTransporteNaoSuporta explícito.
func (c *clienteGRPC) BaixarMidia(_ context.Context, _ string) (io.ReadCloser, error) {
	return nil, novoErroAPI(ErrTransporteNaoSuporta, "BaixarMidia: use o transporte REST")
}

// InjetarMidia faz o Dublê servir estes bytes na URL dada, uma vez só (como a URL de capacidade do gateway).
func (s *sandboxCliente) InjetarMidia(urlMidia string, dados []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.midias[urlMidia] = bytes.Clone(dados)
}

// BaixarMidia (Dublê) devolve a mídia injetada e a consome; URL desconhecida ou já usada = ErrNaoEncontrado.
func (s *sandboxCliente) BaixarMidia(_ context.Context, urlMidia string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checarErro(); err != nil {
		return nil, err
	}
	dados, ok := s.midias[urlMidia]
	if !ok {
		return nil, novoErroAPI(ErrNaoEncontrado, "midia vencida, ja usada ou inexistente")
	}
	delete(s.midias, urlMidia)
	return io.NopCloser(bytes.NewReader(dados)), nil
}
