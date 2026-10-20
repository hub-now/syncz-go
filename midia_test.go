package syncz_test

// Mídia recebida (task 142): media.ready e media.failed decodificam tipados (com o wa_message_id), e BaixarMidia baixa
// pela URL de capacidade no REST (sem a chave de API, só no host do gateway), no Dublê (uma vez) e recusa no gRPC.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	syncz "github.com/hub-now/syncz-go"
)

func TestMidia_EventosTipados(t *testing.T) {
	ev, err := syncz.DecodificarEvento([]byte(`{"type":"media.ready","payload":{"media_id":"m1","wa_message_id":"WA1",
		"url":"/media/d/tok","mimetype":"application/pdf","size":42,"expires_at":"2026-10-20T10:15:00Z"}}`))
	if err != nil {
		t.Fatalf("DecodificarEvento: %v", err)
	}
	p, ok := ev.PayloadTipado.(*syncz.PayloadMediaReady)
	if !ok || p.WAMensagemID != "WA1" || p.URL != "/media/d/tok" || p.Tamanho != 42 || p.ExpiraEm.IsZero() {
		t.Fatalf("media.ready tipado = %#v", ev.PayloadTipado)
	}
	ev, err = syncz.DecodificarEvento([]byte(`{"type":"media.failed","payload":{"media_id":"m1","wa_message_id":"WA1","reason":"too_large"}}`))
	if f, ok := ev.PayloadTipado.(*syncz.PayloadMediaFailed); err != nil || !ok || f.WAMensagemID != "WA1" || f.Motivo != "too_large" {
		t.Fatalf("media.failed tipado = %#v (%v)", ev.PayloadTipado, err)
	}
}

func TestMidia_BaixarREST(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("BaixarMidia mandou Authorization: a URL de capacidade é a credencial")
		}
		if r.URL.Path != "/media/d/tok" {
			http.Error(w, "nao encontrado", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte("%PDF"))
	}))
	defer ts.Close()
	cli, err := syncz.Novo(syncz.Opcoes{BaseURL: ts.URL, ChaveAPI: "secret-token", HTTPClient: ts.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, u := range []string{"/media/d/tok", ts.URL + "/media/d/tok"} {
		corpo, err := cli.BaixarMidia(ctx, u)
		if err != nil {
			t.Fatalf("BaixarMidia(%s): %v", u, err)
		}
		b, _ := io.ReadAll(corpo)
		_ = corpo.Close()
		if string(b) != "%PDF" {
			t.Errorf("BaixarMidia(%s) = %q", u, b)
		}
	}
	if _, err := cli.BaixarMidia(ctx, "/media/d/vencido"); !errors.Is(err, syncz.ErrNaoEncontrado) {
		t.Errorf("link vencido: queria ErrNaoEncontrado, veio %v", err)
	}
	for _, u := range []string{"https://outro.exemplo/media/d/tok", "//outro.exemplo/media/d/tok"} {
		if _, err := cli.BaixarMidia(ctx, u); !errors.Is(err, syncz.ErrInvalido) {
			t.Errorf("BaixarMidia(%s) fora do gateway: queria ErrInvalido, veio %v", u, err)
		}
	}
}

func TestMidia_BaixarDubleEGRPC(t *testing.T) {
	ctx := context.Background()
	sbx := syncz.NovoSandbox()
	sbx.InjetarMidia("/media/d/tok", []byte("png"))
	corpo, err := sbx.BaixarMidia(ctx, "/media/d/tok")
	if err != nil {
		t.Fatalf("Dublê: %v", err)
	}
	if b, _ := io.ReadAll(corpo); string(b) != "png" {
		t.Errorf("Dublê devolveu %q", b)
	}
	if _, err := sbx.BaixarMidia(ctx, "/media/d/tok"); !errors.Is(err, syncz.ErrNaoEncontrado) {
		t.Errorf("Dublê: a URL é de uso único; o 2º download devia dar ErrNaoEncontrado, veio %v", err)
	}
	grpc := syncz.NovoClienteGRPCComConn(nil, func() error { return nil })
	if _, err := grpc.BaixarMidia(ctx, "/media/d/tok"); !errors.Is(err, syncz.ErrTransporteNaoSuporta) {
		t.Errorf("gRPC: queria ErrTransporteNaoSuporta, veio %v", err)
	}
}
