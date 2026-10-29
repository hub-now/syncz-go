package syncz_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	syncz "github.com/hub-now/syncz-go"
)

// clienteFalso implementa só NovoLinkWizard (o handler não chama mais nada);
// os demais métodos vêm do Cliente embutido, que fica nil de propósito.
type clienteFalso struct {
	syncz.Cliente
	chamadas int
	erro     error
}

func (c *clienteFalso) NovoLinkWizard(_ context.Context, instanciaID string) (syncz.LinkWizard, error) {
	c.chamadas++
	if c.erro != nil {
		return syncz.LinkWizard{}, c.erro
	}
	return syncz.LinkWizard{
		InstanciaID: instanciaID,
		URL:         fmt.Sprintf("https://sync.exemplo/connect/link-%d", c.chamadas),
	}, nil
}

// pedir faz GET no handler e devolve a resposta gravada.
func pedir(h http.Handler, alvo string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, alvo, nil))
	return rec
}

// resolverFixo devolve sempre a mesma instância (o handler não se importa com a origem).
func resolverFixo(id string) func(*http.Request) (string, error) {
	return func(*http.Request) (string, error) { return id, nil }
}

// TestHandlerReconectar_302ComLinkNovoACadaClique (task 192, CA-12): cada clique
// gera um link novo na API (nada de cache) e responde 302 para ele.
func TestHandlerReconectar_302ComLinkNovoACadaClique(t *testing.T) {
	cli := &clienteFalso{}
	h := syncz.HandlerReconectar(cli, resolverFixo("inst-1"))

	primeiro := pedir(h, "/reconectar")
	segundo := pedir(h, "/reconectar")

	for i, rec := range []*httptest.ResponseRecorder{primeiro, segundo} {
		if rec.Code != http.StatusFound {
			t.Fatalf("clique %d: status %d, esperado 302", i+1, rec.Code)
		}
	}
	if cli.chamadas != 2 {
		t.Fatalf("NovoLinkWizard chamado %d vezes, esperado 2 (um por clique)", cli.chamadas)
	}
	if primeiro.Header().Get("Location") == segundo.Header().Get("Location") {
		t.Fatalf("dois cliques devolveram o mesmo link: %q", primeiro.Header().Get("Location"))
	}
	if loc := primeiro.Header().Get("Location"); loc != "https://sync.exemplo/connect/link-1" {
		t.Fatalf("Location = %q, esperado o link devolvido pela API", loc)
	}
}

// TestHandlerReconectar_404 (CA-12): resolver com erro, instância vazia e
// instância inexistente na API respondem 404 em texto simples.
func TestHandlerReconectar_404(t *testing.T) {
	t.Run("resolver falha", func(t *testing.T) {
		cli := &clienteFalso{}
		h := syncz.HandlerReconectar(cli, func(*http.Request) (string, error) {
			return "", errors.New("sem parametro")
		})
		if rec := pedir(h, "/x"); rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, esperado 404", rec.Code)
		}
		if cli.chamadas != 0 {
			t.Fatalf("API chamada mesmo com resolver falho")
		}
	})

	t.Run("instancia vazia", func(t *testing.T) {
		cli := &clienteFalso{}
		h := syncz.HandlerReconectar(cli, resolverFixo(""))
		if rec := pedir(h, "/x"); rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, esperado 404", rec.Code)
		}
		if cli.chamadas != 0 {
			t.Fatalf("API chamada com instância vazia")
		}
	})

	t.Run("API responde 404", func(t *testing.T) {
		cli := &clienteFalso{erro: syncz.ErrNaoEncontrado}
		h := syncz.HandlerReconectar(cli, resolverFixo("inst-sumiu"))
		rec := pedir(h, "/x")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status %d, esperado 404", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "" {
			t.Fatalf("404 não pode redirecionar, Location = %q", loc)
		}
	})
}

// TestHandlerReconectar_502 (CA-12): erro da API que não é 404 (ex.: 500 ou
// falha de rede) vira 502 com texto curto, sem vazar o detalhe interno.
func TestHandlerReconectar_502(t *testing.T) {
	detalhe := "dial tcp 10.0.0.5:8443: connection refused"
	cli := &clienteFalso{erro: errors.New(detalhe)}
	h := syncz.HandlerReconectar(cli, resolverFixo("inst-1"))

	rec := pedir(h, "/x")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, esperado 502", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.5") || strings.Contains(rec.Body.String(), "connection refused") {
		t.Fatalf("502 vazou detalhe interno: %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Fatalf("Content-Type = %q, esperado texto simples", ct)
	}
}

// TestAssinarReconexao_ResolverAssinado (task 192, CA-12): o link longo do
// tenant leva o token assinado; o token íntegro resolve a instância, e
// token adulterado ou com outro segredo não resolve.
func TestAssinarReconexao_ResolverAssinado(t *testing.T) {
	segredo := []byte("segredo-do-tenant")
	token := syncz.AssinarReconexao(segredo, "inst-1")
	resolver := syncz.ResolverAssinado(segredo, "t")

	id, err := resolver(httptest.NewRequest(http.MethodGet, "/reconectar?t="+token, nil))
	if err != nil || id != "inst-1" {
		t.Fatalf("token íntegro: id=%q err=%v, esperado inst-1", id, err)
	}

	adulterado := strings.Replace(token, "inst-1.", "inst-2.", 1)
	if _, err := resolver(httptest.NewRequest(http.MethodGet, "/reconectar?t="+adulterado, nil)); err == nil {
		t.Fatal("token com instância trocada foi aceito")
	}

	outroSegredo := syncz.ResolverAssinado([]byte("outro"), "t")
	if _, err := outroSegredo(httptest.NewRequest(http.MethodGet, "/reconectar?t="+token, nil)); err == nil {
		t.Fatal("token assinado com outro segredo foi aceito")
	}

	if _, err := resolver(httptest.NewRequest(http.MethodGet, "/reconectar", nil)); err == nil {
		t.Fatal("requisição sem o parâmetro foi aceita")
	}
}

// TestHandlerReconectar_ComResolverAssinado: a ponta a ponta do link longo do
// tenant: token íntegro → 302; token adulterado → 404 (sem chamar a API).
func TestHandlerReconectar_ComResolverAssinado(t *testing.T) {
	segredo := []byte("segredo-do-tenant")
	cli := &clienteFalso{}
	h := syncz.HandlerReconectar(cli, syncz.ResolverAssinado(segredo, "t"))

	token := syncz.AssinarReconexao(segredo, "inst-1")
	if rec := pedir(h, "/reconectar?t="+token); rec.Code != http.StatusFound {
		t.Fatalf("token íntegro: status %d, esperado 302", rec.Code)
	}

	adulterado := strings.Replace(token, "inst-1.", "inst-2.", 1)
	if rec := pedir(h, "/reconectar?t="+adulterado); rec.Code != http.StatusNotFound {
		t.Fatalf("token adulterado: status %d, esperado 404", rec.Code)
	}
	if cli.chamadas != 1 {
		t.Fatalf("API chamada %d vezes, esperado 1 (só o token íntegro)", cli.chamadas)
	}
}
