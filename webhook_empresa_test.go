package syncz_test

// Webhook da empresa pela chave (spec empresa-sem-webhook, CA-12): DefinirWebhook, ConsultarWebhook, ApagarWebhook,
// TestarWebhook e RotacionarSegredoWebhook sobre /api/v1/webhook*; o segredo só volta na criação e na rotação; no gRPC os
// cinco devolvem ErrTransporteNaoSuporta.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	syncz "github.com/hub-now/syncz-go"
)

// servidorWebhookEmpresa imita as rotas /api/v1/webhook* com o mesmo corpo do servidor (adminWebhookResp).
func servidorWebhookEmpresa(t *testing.T) *httptest.Server {
	t.Helper()
	var atual map[string]any
	responder := func(w http.ResponseWriter, extra map[string]any) {
		corpo := map[string]any{}
		for k, v := range atual {
			corpo[k] = v
		}
		for k, v := range extra {
			corpo[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(corpo)
	}
	semWebhook := func(w http.ResponseWriter) bool {
		if atual != nil {
			return false
		}
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"title":"nao encontrado","detail":"empresa sem webhook"}`))
		return true
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer chave-empresa" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "PUT /api/v1/webhook":
			var req struct {
				URL        string   `json:"url"`
				EventTypes []string `json:"event_types"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.URL == "http://inseguro" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"title":"invalido","field":"url"}`))
				return
			}
			atual = map[string]any{"id": "w1", "url": req.URL, "status": "untested", "event_types": req.EventTypes,
				"created_at": "2026-10-21T23:13:00Z"}
			responder(w, map[string]any{"secret": "s1"})
		case "GET /api/v1/webhook":
			if !semWebhook(w) {
				responder(w, nil)
			}
		case "DELETE /api/v1/webhook":
			if !semWebhook(w) {
				atual = nil
				w.WriteHeader(http.StatusNoContent)
			}
		case "POST /api/v1/webhook/test":
			if !semWebhook(w) {
				atual["status"] = "active"
				atual["verified_at"] = "2026-10-21T23:14:00Z"
				responder(w, map[string]any{"test_status_code": 200})
			}
		case "POST /api/v1/webhook/rotate-secret":
			if !semWebhook(w) {
				atual["secret_rotated_at"] = "2026-10-21T23:15:00Z"
				responder(w, map[string]any{"secret": "s2"})
			}
		default:
			http.Error(w, "rota errada", http.StatusTeapot)
		}
	}))
}

func TestWebhookEmpresa_REST(t *testing.T) {
	ts := servidorWebhookEmpresa(t)
	defer ts.Close()
	cli, err := syncz.Novo(syncz.Opcoes{BaseURL: ts.URL, ChaveAPI: "chave-empresa", HTTPClient: ts.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := cli.ConsultarWebhook(ctx); !errors.Is(err, syncz.ErrNaoEncontrado) {
		t.Fatalf("sem webhook: queria ErrNaoEncontrado, veio %v", err)
	}
	if _, err := cli.DefinirWebhook(ctx, "http://inseguro", nil); !errors.Is(err, syncz.ErrInvalido) {
		t.Fatalf("url sem https: queria ErrInvalido, veio %v", err)
	}
	w, err := cli.DefinirWebhook(ctx, "https://erp.exemplo.com/syncz", []string{"message.received"})
	if err != nil || w.ID != "w1" || w.Status != "untested" || w.Segredo != "s1" || w.CriadoEm.IsZero() ||
		len(w.TiposEvento) != 1 || w.TiposEvento[0] != "message.received" {
		t.Fatalf("DefinirWebhook = %+v (%v)", w, err)
	}
	w, err = cli.ConsultarWebhook(ctx)
	if err != nil || w.URL != "https://erp.exemplo.com/syncz" || w.Segredo != "" {
		t.Fatalf("ConsultarWebhook = %+v (%v); o segredo nunca volta no GET", w, err)
	}
	w, err = cli.TestarWebhook(ctx)
	if err != nil || w.Status != "active" || w.StatusTeste != 200 || w.VerificadoEm.IsZero() || w.Segredo != "" {
		t.Fatalf("TestarWebhook = %+v (%v)", w, err)
	}
	w, err = cli.RotacionarSegredoWebhook(ctx)
	if err != nil || w.Segredo != "s2" || w.SegredoRotacionadoEm.IsZero() {
		t.Fatalf("RotacionarSegredoWebhook = %+v (%v)", w, err)
	}
	if err := cli.ApagarWebhook(ctx); err != nil {
		t.Fatalf("ApagarWebhook: %v", err)
	}
	if _, err := cli.ConsultarWebhook(ctx); !errors.Is(err, syncz.ErrNaoEncontrado) {
		t.Fatalf("depois de apagar: queria ErrNaoEncontrado, veio %v", err)
	}
	if err := cli.ApagarWebhook(ctx); !errors.Is(err, syncz.ErrNaoEncontrado) {
		t.Fatalf("apagar de novo: queria ErrNaoEncontrado, veio %v", err)
	}
}

func TestWebhookEmpresa_Duble(t *testing.T) {
	ctx := context.Background()
	sbx := syncz.NovoSandbox()
	if _, err := sbx.ConsultarWebhook(ctx); !errors.Is(err, syncz.ErrNaoEncontrado) {
		t.Fatalf("sem webhook: %v", err)
	}
	if _, err := sbx.DefinirWebhook(ctx, "http://x", nil); !errors.Is(err, syncz.ErrInvalido) {
		t.Fatalf("http: %v", err)
	}
	w, err := sbx.DefinirWebhook(ctx, "https://erp.exemplo.com/syncz", nil)
	if err != nil || len(w.Segredo) != 64 || w.Status != "untested" {
		t.Fatalf("Definir = %+v (%v)", w, err)
	}
	if w, _ := sbx.ConsultarWebhook(ctx); w.Segredo != "" {
		t.Fatal("Dublê devolveu o segredo no Consultar")
	}
	if w, _ := sbx.TestarWebhook(ctx); w.Status != "active" || w.StatusTeste != 200 {
		t.Fatalf("Testar = %+v", w)
	}
	if w2, _ := sbx.RotacionarSegredoWebhook(ctx); w2.Segredo == "" || w2.Segredo == w.Segredo {
		t.Fatalf("Rotacionar = %+v", w2)
	}
	if err := sbx.ApagarWebhook(ctx); err != nil {
		t.Fatal(err)
	}
	if err := sbx.ApagarWebhook(ctx); !errors.Is(err, syncz.ErrNaoEncontrado) {
		t.Fatalf("apagar de novo: %v", err)
	}
}

func TestWebhookEmpresa_GRPCNaoSuporta(t *testing.T) {
	ctx := context.Background()
	g := syncz.NovoClienteGRPCComConn(nil, func() error { return nil })
	_, e1 := g.DefinirWebhook(ctx, "https://x", nil)
	_, e2 := g.ConsultarWebhook(ctx)
	e3 := g.ApagarWebhook(ctx)
	_, e4 := g.TestarWebhook(ctx)
	_, e5 := g.RotacionarSegredoWebhook(ctx)
	for i, err := range []error{e1, e2, e3, e4, e5} {
		if !errors.Is(err, syncz.ErrTransporteNaoSuporta) {
			t.Errorf("método %d: queria ErrTransporteNaoSuporta, veio %v", i+1, err)
		}
	}
}
