package syncz

// Empresa sem webhook (spec empresa-sem-webhook, CA-12): a empresa só precisa da URL base e da chave de API. Ela consulta os
// eventos de todas as instâncias por GET /api/v1/events/stream sem instance_id (janela `since` ou cursor `after`, o `id:` de cada
// evento) e, se quiser receber por webhook, cadastra o próprio em /api/v1/webhook. Só o transporte REST fala com essas rotas; o
// gRPC devolve ErrTransporteNaoSuporta e o Dublê guarda tudo em memória.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// LoteMaximoEventos é o teto de eventos que o servidor devolve por consulta (RN-03). Lote cheio = pode haver mais: consulte de
// novo com Apos = UltimoCursor.
const LoteMaximoEventos = 1000

// janelasConsulta são os únicos valores de Desde que o servidor aceita (`since`).
var janelasConsulta = map[time.Duration]string{
	20 * time.Minute: "20m",
	30 * time.Minute: "30m",
	time.Hour:        "1h",
	2 * time.Hour:    "2h",
	24 * time.Hour:   "24h",
}

// OpcoesConsultaEventos escolhe o começo da consulta: Desde (janela de tempo) OU Apos (cursor), nunca os dois. Sem nenhum, a
// janela é de 24h (tudo o que o gateway ainda guarda).
type OpcoesConsultaEventos struct {
	// Desde: 20m, 30m, 1h, 2h ou 24h. Outro valor → ErrInvalido sem ir à rede.
	Desde time.Duration
	// Apos: o Cursor do último evento já processado; volta só os de cursor maior, sem repetir.
	Apos int64
}

// EventoEmpresa é um evento da empresa com o seu cursor (o `id:` do SSE, crescente).
type EventoEmpresa struct {
	Cursor int64
	Evento Evento
}

// ConsultaEventos é o resultado de ConsultarEventos.
type ConsultaEventos struct {
	Eventos []EventoEmpresa
	// UltimoCursor: o cursor do último evento devolvido (ou o Apos pedido, se não veio nenhum). Guarde-o para a próxima consulta.
	UltimoCursor int64
	// TemMais: o lote veio cheio (LoteMaximoEventos); consulte de novo com Apos = UltimoCursor.
	TemMais bool
}

// WebhookEmpresa é o webhook da empresa da chave. Segredo só vem preenchido em DefinirWebhook e RotacionarSegredoWebhook (uma
// vez: guarde-o para verificar a assinatura com VerificarWebhook); StatusTeste só em TestarWebhook (0 = sem resposta).
type WebhookEmpresa struct {
	ID                   string
	URL                  string
	Status               string // untested ao cadastrar; active depois de um teste 2xx
	TiposEvento          []string
	CriadoEm             time.Time
	VerificadoEm         time.Time
	SegredoRotacionadoEm time.Time
	Segredo              string
	StatusTeste          int
}

// parametrosConsulta valida as opções do lado do cliente (o servidor valida de novo) e monta a query string.
func parametrosConsulta(o OpcoesConsultaEventos) (url.Values, error) {
	q := url.Values{}
	if o.Desde != 0 && o.Apos != 0 {
		return nil, novoErroAPI(ErrInvalido, "use Desde ou Apos, nao os dois")
	}
	if o.Apos < 0 {
		return nil, novoErroAPI(ErrInvalido, "Apos nao pode ser negativo")
	}
	if o.Apos > 0 {
		q.Set("after", strconv.FormatInt(o.Apos, 10))
	}
	if o.Desde != 0 {
		since, ok := janelasConsulta[o.Desde]
		if !ok {
			return nil, novoErroAPI(ErrInvalido, "Desde: use 20m, 30m, 1h, 2h ou 24h")
		}
		q.Set("since", since)
	}
	return q, nil
}

// fecharConsulta preenche UltimoCursor e TemMais a partir dos eventos lidos.
func fecharConsulta(eventos []EventoEmpresa, apos int64) ConsultaEventos {
	r := ConsultaEventos{Eventos: eventos, UltimoCursor: apos, TemMais: len(eventos) >= LoteMaximoEventos}
	if n := len(eventos); n > 0 {
		r.UltimoCursor = eventos[n-1].Cursor
	}
	return r
}

// ConsultarEventos (REST) lê os eventos de todas as instâncias da empresa da chave, em ordem crescente de cursor, no máximo
// LoteMaximoEventos por chamada.
func (c *clienteREST) ConsultarEventos(ctx context.Context, o OpcoesConsultaEventos) (ConsultaEventos, error) {
	q, err := parametrosConsulta(o)
	if err != nil {
		return ConsultaEventos{}, err
	}
	u := c.baseURL + "/api/v1/events/stream"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ConsultaEventos{}, fmt.Errorf("syncz: criar requisicao consulta eventos: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.chaveAPI)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ConsultaEventos{}, fmt.Errorf("syncz: erro de rede: %w", err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		corpo, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return ConsultaEventos{}, erroPorStatusHTTP(resp.StatusCode, corpo)
	}

	var eventos []EventoEmpresa
	var errLeitura error
	lerBlocosSSE(ctx, resp.Body, func(b sseBloco) bool {
		if strings.TrimSpace(b.dados) == "" {
			return true
		}
		cursor, err := strconv.ParseInt(b.id, 10, 64)
		if err != nil {
			errLeitura = fmt.Errorf("syncz: evento sem cursor valido (id: %q)", b.id)
			return false
		}
		ev, err := DecodificarEvento([]byte(b.dados))
		if err != nil {
			errLeitura = err
			return false
		}
		eventos = append(eventos, EventoEmpresa{Cursor: cursor, Evento: ev})
		return true
	}, func(err error) {
		errLeitura = fmt.Errorf("syncz: consulta eventos: %w", err)
	})
	if errLeitura != nil {
		return ConsultaEventos{}, errLeitura
	}
	if err := ctx.Err(); err != nil {
		return ConsultaEventos{}, err
	}
	return fecharConsulta(eventos, o.Apos), nil
}

// webhookEmpresaResp espelha o corpo das rotas /api/v1/webhook* (internal/modulos/rest/admin_webhook.go, adminWebhookResp).
type webhookEmpresaResp struct {
	ID              string   `json:"id"`
	URL             string   `json:"url"`
	Status          string   `json:"status"`
	EventTypes      []string `json:"event_types"`
	VerifiedAt      string   `json:"verified_at"`
	SecretRotatedAt string   `json:"secret_rotated_at"`
	CreatedAt       string   `json:"created_at"`
	Secret          string   `json:"secret"`
	TestStatusCode  int      `json:"test_status_code"`
}

func (r webhookEmpresaResp) paraSDK() WebhookEmpresa {
	return WebhookEmpresa{
		ID:                   r.ID,
		URL:                  r.URL,
		Status:               r.Status,
		TiposEvento:          r.EventTypes,
		CriadoEm:             parseTimestamp(r.CreatedAt),
		VerificadoEm:         parseTimestamp(r.VerifiedAt),
		SegredoRotacionadoEm: parseTimestamp(r.SecretRotatedAt),
		Segredo:              r.Secret,
		StatusTeste:          r.TestStatusCode,
	}
}

func (c *clienteREST) webhookEmpresa(ctx context.Context, metodo, path string, corpo any) (WebhookEmpresa, error) {
	_, respBytes, err := c.requisicao(ctx, metodo, path, corpo, "")
	if err != nil {
		return WebhookEmpresa{}, err
	}
	var r webhookEmpresaResp
	if err := json.Unmarshal(respBytes, &r); err != nil {
		return WebhookEmpresa{}, fmt.Errorf("syncz: desserializar webhook: %w", err)
	}
	return r.paraSDK(), nil
}

// DefinirWebhook (REST) cadastra ou troca o webhook da empresa (PUT /api/v1/webhook). tiposEvento vazio = todos os tipos. URL
// sem https ou de host não público → ErrInvalido. O Segredo volta uma única vez.
func (c *clienteREST) DefinirWebhook(ctx context.Context, urlWebhook string, tiposEvento []string) (WebhookEmpresa, error) {
	corpo := struct {
		URL        string   `json:"url"`
		EventTypes []string `json:"event_types,omitempty"`
	}{URL: urlWebhook, EventTypes: tiposEvento}
	return c.webhookEmpresa(ctx, http.MethodPut, "/api/v1/webhook", corpo)
}

// ConsultarWebhook (REST) devolve o webhook da empresa, sem o segredo; sem webhook → ErrNaoEncontrado.
func (c *clienteREST) ConsultarWebhook(ctx context.Context) (WebhookEmpresa, error) {
	return c.webhookEmpresa(ctx, http.MethodGet, "/api/v1/webhook", nil)
}

// ApagarWebhook (REST) apaga o webhook da empresa; os eventos seguintes ficam só em ConsultarEventos. Sem webhook →
// ErrNaoEncontrado.
func (c *clienteREST) ApagarWebhook(ctx context.Context) error {
	_, _, err := c.requisicao(ctx, http.MethodDelete, "/api/v1/webhook", nil, "")
	return err
}

// TestarWebhook (REST) manda uma entrega assinada de teste; StatusTeste traz o status HTTP que o webhook respondeu e um 2xx
// deixa o Status em active. Destino recusado pela trava de SSRF → ErrInvalido; sem webhook → ErrNaoEncontrado.
func (c *clienteREST) TestarWebhook(ctx context.Context) (WebhookEmpresa, error) {
	return c.webhookEmpresa(ctx, http.MethodPost, "/api/v1/webhook/test", nil)
}

// RotacionarSegredoWebhook (REST) gera um segredo novo (devolvido uma vez); o anterior segue valendo na janela de rotação.
func (c *clienteREST) RotacionarSegredoWebhook(ctx context.Context) (WebhookEmpresa, error) {
	return c.webhookEmpresa(ctx, http.MethodPost, "/api/v1/webhook/rotate-secret", nil)
}

// gRPC: o proto não tem RPC para a consulta da empresa nem para o webhook pela chave. Mesmo critério de BaixarMidia.

func (c *clienteGRPC) ConsultarEventos(_ context.Context, _ OpcoesConsultaEventos) (ConsultaEventos, error) {
	return ConsultaEventos{}, novoErroAPI(ErrTransporteNaoSuporta, "ConsultarEventos: use o transporte REST")
}

func (c *clienteGRPC) DefinirWebhook(_ context.Context, _ string, _ []string) (WebhookEmpresa, error) {
	return WebhookEmpresa{}, novoErroAPI(ErrTransporteNaoSuporta, "DefinirWebhook: use o transporte REST")
}

func (c *clienteGRPC) ConsultarWebhook(_ context.Context) (WebhookEmpresa, error) {
	return WebhookEmpresa{}, novoErroAPI(ErrTransporteNaoSuporta, "ConsultarWebhook: use o transporte REST")
}

func (c *clienteGRPC) ApagarWebhook(_ context.Context) error {
	return novoErroAPI(ErrTransporteNaoSuporta, "ApagarWebhook: use o transporte REST")
}

func (c *clienteGRPC) TestarWebhook(_ context.Context) (WebhookEmpresa, error) {
	return WebhookEmpresa{}, novoErroAPI(ErrTransporteNaoSuporta, "TestarWebhook: use o transporte REST")
}

func (c *clienteGRPC) RotacionarSegredoWebhook(_ context.Context) (WebhookEmpresa, error) {
	return WebhookEmpresa{}, novoErroAPI(ErrTransporteNaoSuporta, "RotacionarSegredoWebhook: use o transporte REST")
}

// Dublê: eventos injetados ficam numa lista com cursor crescente; o webhook fica em memória (sem entrega de verdade).

// InjetarEventoEmpresa põe um evento na consulta da empresa e devolve o cursor que ele recebeu.
func (s *sandboxCliente) InjetarEventoEmpresa(ev Evento) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.OcorridoEm.IsZero() {
		ev.OcorridoEm = time.Now().UTC()
	}
	s.cursorEmpresa++
	s.eventosEmpresa = append(s.eventosEmpresa, EventoEmpresa{Cursor: s.cursorEmpresa, Evento: ev})
	return s.cursorEmpresa
}

// ConsultarEventos (Dublê) aplica as mesmas regras do servidor: janela ou cursor, ordem crescente, lote de LoteMaximoEventos.
func (s *sandboxCliente) ConsultarEventos(_ context.Context, o OpcoesConsultaEventos) (ConsultaEventos, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checarErro(); err != nil {
		return ConsultaEventos{}, err
	}
	if _, err := parametrosConsulta(o); err != nil {
		return ConsultaEventos{}, err
	}
	janela := o.Desde
	if janela == 0 {
		janela = 24 * time.Hour
	}
	limite := time.Now().Add(-janela)
	var eventos []EventoEmpresa
	for _, e := range s.eventosEmpresa {
		if len(eventos) == LoteMaximoEventos {
			break
		}
		if o.Apos > 0 {
			if e.Cursor <= o.Apos {
				continue
			}
		} else if e.Evento.OcorridoEm.Before(limite) {
			continue
		}
		eventos = append(eventos, e)
	}
	return fecharConsulta(eventos, o.Apos), nil
}

func segredoDuble() string {
	var b [32]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read não falha (Go 1.24+)
	return hex.EncodeToString(b[:])
}

// DefinirWebhook (Dublê) aceita só https e guarda o webhook com status untested; o segredo volta uma vez.
func (s *sandboxCliente) DefinirWebhook(_ context.Context, urlWebhook string, tiposEvento []string) (WebhookEmpresa, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checarErro(); err != nil {
		return WebhookEmpresa{}, err
	}
	u, err := url.Parse(urlWebhook)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return WebhookEmpresa{}, novoErroAPI(ErrInvalido, "url: use https com host publico")
	}
	s.seqWebhook++
	w := WebhookEmpresa{
		ID:          fmt.Sprintf("sbx-webhook-%d", s.seqWebhook),
		URL:         urlWebhook,
		Status:      "untested",
		TiposEvento: append([]string{}, tiposEvento...),
		CriadoEm:    time.Now().UTC(),
	}
	guardado := w
	s.webhook = &guardado
	w.Segredo = segredoDuble()
	return w, nil
}

// webhookDuble devolve uma cópia do webhook guardado (sem segredo) ou ErrNaoEncontrado. Chame com s.mu travado.
func (s *sandboxCliente) webhookDuble() (WebhookEmpresa, error) {
	if err := s.checarErro(); err != nil {
		return WebhookEmpresa{}, err
	}
	if s.webhook == nil {
		return WebhookEmpresa{}, novoErroAPI(ErrNaoEncontrado, "empresa sem webhook")
	}
	w := *s.webhook
	w.TiposEvento = append([]string{}, s.webhook.TiposEvento...)
	return w, nil
}

// ConsultarWebhook (Dublê) devolve o webhook guardado, sem segredo.
func (s *sandboxCliente) ConsultarWebhook(_ context.Context) (WebhookEmpresa, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.webhookDuble()
}

// ApagarWebhook (Dublê) esquece o webhook; sem webhook → ErrNaoEncontrado.
func (s *sandboxCliente) ApagarWebhook(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.webhookDuble(); err != nil {
		return err
	}
	s.webhook = nil
	return nil
}

// TestarWebhook (Dublê) simula uma entrega 200: o webhook passa a active.
func (s *sandboxCliente) TestarWebhook(_ context.Context) (WebhookEmpresa, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.webhookDuble(); err != nil {
		return WebhookEmpresa{}, err
	}
	s.webhook.Status = "active"
	s.webhook.VerificadoEm = time.Now().UTC()
	w, _ := s.webhookDuble()
	w.StatusTeste = http.StatusOK
	return w, nil
}

// RotacionarSegredoWebhook (Dublê) devolve um segredo novo uma vez.
func (s *sandboxCliente) RotacionarSegredoWebhook(_ context.Context) (WebhookEmpresa, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.webhookDuble(); err != nil {
		return WebhookEmpresa{}, err
	}
	s.webhook.SegredoRotacionadoEm = time.Now().UTC()
	w, _ := s.webhookDuble()
	w.Segredo = segredoDuble()
	return w, nil
}
