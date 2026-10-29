package syncz_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	syncz "github.com/hub-now/syncz-go"
)

// TestParearComTelefoneREST (task 192, CA-11, spec reconectar-mesma-instancia):
// o telefone vai no query param ?phone= escapado, e a resposta vira Pareamento
// pelo mesmo parse de EstadoPareamento. Sem telefone, EstadoPareamento não manda ?phone=.
func TestParearComTelefoneREST(t *testing.T) {
	var phoneRecebido []string
	var temPhone []bool

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/instances/{id}/pairing", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "nao-existe" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": "instancia inexistente"})
			return
		}
		if r.PathValue("id") == "fone-ruim" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"detail": "telefone invalido"})
			return
		}
		phoneRecebido = append(phoneRecebido, r.URL.Query().Get("phone"))
		temPhone = append(temPhone, r.URL.Query().Has("phone"))
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"qr_code":    "2@qr-telefone",
			"pair_code":  "ABCD-1234",
			"seq":        1,
			"expires_at": "2026-10-29T17:40:00-03:00",
			"paired":     false,
		})
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	cli, err := syncz.Novo(syncz.Opcoes{BaseURL: ts.URL, ChaveAPI: "secret-token", HTTPClient: ts.Client()})
	if err != nil {
		t.Fatalf("syncz.Novo: %v", err)
	}
	defer func() { _ = cli.Fechar() }()
	ctx := context.Background()

	par, err := cli.ParearComTelefone(ctx, "inst-1", "5531999995443")
	if err != nil {
		t.Fatalf("ParearComTelefone falhou: %v", err)
	}
	if par.PairCode != "ABCD-1234" || par.QRCode != "2@qr-telefone" || par.Pareado {
		t.Fatalf("Pareamento incorreto: %+v", par)
	}
	if len(phoneRecebido) != 1 || phoneRecebido[0] != "5531999995443" {
		t.Fatalf("servidor recebeu phone=%v, esperado [5531999995443]", phoneRecebido)
	}

	// Sem telefone: EstadoPareamento não manda o query param.
	if _, err := cli.EstadoPareamento(ctx, "inst-1"); err != nil {
		t.Fatalf("EstadoPareamento falhou: %v", err)
	}
	if len(temPhone) != 2 || temPhone[1] {
		t.Fatalf("EstadoPareamento não deveria mandar ?phone=: %v", temPhone)
	}

	if _, err := cli.ParearComTelefone(ctx, "nao-existe", "5531999995443"); !errors.Is(err, syncz.ErrNaoEncontrado) {
		t.Errorf("esperava ErrNaoEncontrado, obteve: %v", err)
	}
	if _, err := cli.ParearComTelefone(ctx, "fone-ruim", "123"); !errors.Is(err, syncz.ErrInvalido) {
		t.Errorf("esperava ErrInvalido, obteve: %v", err)
	}
}
