package syncz_test

// Empresa sem webhook (spec empresa-sem-webhook, CA-12): ConsultarEventos lê GET /api/v1/events/stream sem instance_id, com o
// cursor de cada evento (a linha id:), por janela (since) ou cursor (after); no gRPC devolve ErrTransporteNaoSuporta.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	syncz "github.com/hub-now/syncz-go"
)

func TestConsultarEventos_REST(t *testing.T) {
	var queries []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/events/stream" || r.URL.Query().Has("instance_id") {
			http.Error(w, "rota errada", http.StatusTeapot)
			return
		}
		if r.Header.Get("Authorization") != "Bearer chave-empresa" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("since") == "5m" {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"title":"invalido","field":"since","detail":"use 20m, 30m, 1h, 2h ou 24h"}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if r.URL.Query().Get("after") == "11" {
			return // nada novo depois do 11
		}
		_, _ = fmt.Fprint(w, "id: 10\ndata: {\"id\":\"e1\",\"type\":\"message.received\",\"instance_id\":\"i1\",\"seq\":3,"+
			"\"occurred_at\":\"2026-10-21T10:00:00Z\",\"payload\":{}}\n\n")
		_, _ = fmt.Fprint(w, "id: 11\ndata: {\"id\":\"e2\",\"type\":\"message.received\",\"instance_id\":\"i2\",\"seq\":1,"+
			"\"occurred_at\":\"2026-10-21T10:01:00Z\",\"payload\":{}}\n\n")
	}))
	defer ts.Close()
	cli, err := syncz.Novo(syncz.Opcoes{BaseURL: ts.URL, ChaveAPI: "chave-empresa", HTTPClient: ts.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	r, err := cli.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{Desde: 2 * time.Hour})
	if err != nil {
		t.Fatalf("Desde 2h: %v", err)
	}
	if len(r.Eventos) != 2 || r.Eventos[0].Cursor != 10 || r.Eventos[1].Cursor != 11 || r.UltimoCursor != 11 || r.TemMais {
		t.Fatalf("consulta = %+v", r)
	}
	if r.Eventos[0].Evento.ID != "e1" || r.Eventos[1].Evento.InstanciaID != "i2" || r.Eventos[0].Evento.OcorridoEm.IsZero() {
		t.Fatalf("eventos mal decodificados: %+v", r.Eventos)
	}

	r, err = cli.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{Apos: r.UltimoCursor})
	if err != nil || len(r.Eventos) != 0 || r.UltimoCursor != 11 {
		t.Fatalf("Apos 11: %+v (%v)", r, err)
	}
	if _, err := cli.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{}); err != nil {
		t.Fatalf("sem opções: %v", err)
	}
	want := []string{"since=2h", "after=11", ""}
	if strings.Join(queries, "|") != strings.Join(want, "|") {
		t.Fatalf("queries = %q, queria %q", queries, want)
	}

	// Validação local: nada vai à rede.
	for _, o := range []syncz.OpcoesConsultaEventos{{Desde: 5 * time.Minute}, {Desde: time.Hour, Apos: 3}, {Apos: -1}} {
		if _, err := cli.ConsultarEventos(ctx, o); !errors.Is(err, syncz.ErrInvalido) {
			t.Errorf("%+v: queria ErrInvalido, veio %v", o, err)
		}
	}
	if len(queries) != 3 {
		t.Fatalf("validação local foi à rede: %q", queries)
	}

	semChave, _ := syncz.Novo(syncz.Opcoes{BaseURL: ts.URL, ChaveAPI: "outra", HTTPClient: ts.Client()})
	if _, err := semChave.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{}); err == nil {
		t.Fatal("chave errada: queria erro")
	}
}

func TestConsultarEventos_LoteCheioTemMais(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 1; i <= syncz.LoteMaximoEventos; i++ {
			_, _ = fmt.Fprintf(w, "id: %d\ndata: {\"id\":\"e%d\",\"type\":\"message.received\"}\n\n", i, i)
		}
	}))
	defer ts.Close()
	cli, _ := syncz.Novo(syncz.Opcoes{BaseURL: ts.URL, ChaveAPI: "k", HTTPClient: ts.Client()})
	r, err := cli.ConsultarEventos(context.Background(), syncz.OpcoesConsultaEventos{})
	if err != nil || !r.TemMais || r.UltimoCursor != syncz.LoteMaximoEventos {
		t.Fatalf("lote cheio: TemMais=%v UltimoCursor=%d (%v)", r.TemMais, r.UltimoCursor, err)
	}
}

func TestConsultarEventos_DubleEGRPC(t *testing.T) {
	ctx := context.Background()
	sbx := syncz.NovoSandbox()
	c1 := sbx.InjetarEventoEmpresa(syncz.Evento{ID: "e1", Tipo: "message.received", InstanciaID: "i1"})
	sbx.InjetarEventoEmpresa(syncz.Evento{ID: "velho", OcorridoEm: time.Now().Add(-3 * time.Hour)})
	c3 := sbx.InjetarEventoEmpresa(syncz.Evento{ID: "e3", InstanciaID: "i2"})

	r, err := sbx.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{Desde: 2 * time.Hour})
	if err != nil || len(r.Eventos) != 2 || r.Eventos[0].Cursor != c1 || r.UltimoCursor != c3 {
		t.Fatalf("Dublê Desde 2h = %+v (%v)", r, err)
	}
	r, err = sbx.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{Apos: c1})
	if err != nil || len(r.Eventos) != 2 || r.Eventos[0].Evento.ID != "velho" {
		t.Fatalf("Dublê Apos = %+v (%v)", r, err)
	}

	grpc := syncz.NovoClienteGRPCComConn(nil, func() error { return nil })
	if _, err := grpc.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{}); !errors.Is(err, syncz.ErrTransporteNaoSuporta) {
		t.Errorf("gRPC: queria ErrTransporteNaoSuporta, veio %v", err)
	}
}
