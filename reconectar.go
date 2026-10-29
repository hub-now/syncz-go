package syncz

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
)

// prefixoReconexao separa a assinatura do link longo de qualquer outro uso do
// mesmo segredo (task 192, CA-12).
const prefixoReconexao = "syncz-reconectar:"

// errTokenReconexao é o erro de ResolverAssinado para qualquer token que não
// confere; não diz qual parte falhou, para não ajudar quem tenta adivinhar.
var errTokenReconexao = errors.New("syncz: token de reconexao invalido")

// HandlerReconectar (task 192, CA-12) é o link longo do tenant: a cada
// requisição gera um link de reconexão novo (10 min) na API e responde 302 para
// ele. Instância inexistente (resolver falho, vazio ou 404 da API) → 404; outro
// erro da API → 502. Não guarda nada em cache: cada clique gera um link.
func HandlerReconectar(cli Cliente, resolver func(*http.Request) (instanciaID string, err error)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		instanciaID, err := resolver(r)
		if err != nil || instanciaID == "" {
			http.Error(w, "instancia nao encontrada", http.StatusNotFound)
			return
		}

		link, err := cli.NovoLinkWizard(r.Context(), instanciaID)
		if err != nil {
			if errors.Is(err, ErrNaoEncontrado) {
				http.Error(w, "instancia nao encontrada", http.StatusNotFound)
				return
			}
			http.Error(w, "nao foi possivel gerar o link de reconexao", http.StatusBadGateway)
			return
		}

		http.Redirect(w, r, link.URL, http.StatusFound)
	})
}

// AssinarReconexao (task 192, CA-12) monta o token do link longo do tenant:
// instanciaID + "." + base64url(HMAC-SHA256(segredo, "syncz-reconectar:"+instanciaID)).
// Não expira: o link longo é do tenant; o que expira é o link de 10 min.
func AssinarReconexao(segredo []byte, instanciaID string) string {
	return instanciaID + "." + base64.RawURLEncoding.EncodeToString(assinaturaReconexao(segredo, instanciaID))
}

// ResolverAssinado (task 192, CA-12) lê o token do query param `parametro`,
// confere a assinatura (hmac.Equal) e devolve o instanciaID. Token ausente,
// malformado ou com assinatura de outro segredo → erro.
func ResolverAssinado(segredo []byte, parametro string) func(*http.Request) (string, error) {
	return func(r *http.Request) (string, error) {
		token := r.URL.Query().Get(parametro)
		// base64url não tem ".", então o último ponto separa o id da assinatura.
		i := strings.LastIndex(token, ".")
		if i <= 0 {
			return "", errTokenReconexao
		}
		instanciaID, sig := token[:i], token[i+1:]

		recebida, err := base64.RawURLEncoding.DecodeString(sig)
		if err != nil || !hmac.Equal(recebida, assinaturaReconexao(segredo, instanciaID)) {
			return "", errTokenReconexao
		}
		return instanciaID, nil
	}
}

func assinaturaReconexao(segredo []byte, instanciaID string) []byte {
	mac := hmac.New(sha256.New, segredo)
	mac.Write([]byte(prefixoReconexao + instanciaID))
	return mac.Sum(nil)
}
