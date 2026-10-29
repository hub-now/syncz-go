package syncz_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	syncz "github.com/hub-now/syncz-go"
	synczv1 "github.com/hub-now/syncz-go/synczv1"
)

// servidorVotoREST sobe um POST .../polls/{poll_id}/vote que guarda o corpo
// recebido e responde com o status e o JSON pedidos.
func servidorVotoREST(t *testing.T, status int, resp any, corpo *json.RawMessage, query *string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/instances/{id}/polls/{poll_id}/vote", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "inst-1" || r.PathValue("poll_id") != "poll-1" {
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
		*query = r.URL.RawQuery
		bruto, _ := io.ReadAll(r.Body)
		*corpo = bruto
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(resp)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestVotarEnquete_REST(t *testing.T) {
	var corpo json.RawMessage
	var query string
	ts := servidorVotoREST(t, http.StatusOK, map[string]any{"poll_id": "poll-1", "options": []string{"chocolate"}}, &corpo, &query)
	cli := pollsClienteREST(t, ts)

	voto, err := cli.VotarEnquete(context.Background(), syncz.EntradaVotarEnquete{
		InstanciaID: "inst-1", PollID: "poll-1", Opcoes: []string{"chocolate"},
	})
	if err != nil {
		t.Fatalf("VotarEnquete falhou: %v", err)
	}
	if voto.PollID != "poll-1" || !reflect.DeepEqual(voto.Opcoes, []string{"chocolate"}) {
		t.Errorf("voto inesperado: %+v", voto)
	}
	if string(corpo) != `{"options":["chocolate"]}` {
		t.Errorf("corpo inesperado: %s", corpo)
	}
	if query == "" {
		t.Errorf("espera síncrona (padrão) deveria ir na query")
	}
}

func TestVotarEnquete_REST_Assincrono(t *testing.T) {
	var corpo json.RawMessage
	query := "x"
	ts := servidorVotoREST(t, http.StatusOK, map[string]any{"poll_id": "poll-1", "options": []string{}}, &corpo, &query)
	cli := pollsClienteREST(t, ts)

	_, err := cli.VotarEnquete(context.Background(), syncz.EntradaVotarEnquete{
		InstanciaID: "inst-1", PollID: "poll-1", EsperaSincrona: syncz.EsperaSincrona{Assincrono: true},
	})
	if err != nil {
		t.Fatalf("VotarEnquete falhou: %v", err)
	}
	if query != "" {
		t.Errorf("assíncrono não deveria enviar query: %q", query)
	}
	// o servidor responde 400 para "options": null; a retirada exige [] exato.
	if string(corpo) != `{"options":[]}` {
		t.Errorf("retirar voto deve enviar options: [] (veio %s)", corpo)
	}
}

func TestVotarEnquete_REST_Erros(t *testing.T) {
	casos := map[string]struct {
		status int
		erro   error
	}{
		"400": {http.StatusBadRequest, syncz.ErrInvalido},
		"404": {http.StatusNotFound, syncz.ErrNaoEncontrado},
		"409": {http.StatusConflict, syncz.ErrConflito},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			var corpo json.RawMessage
			var query string
			ts := servidorVotoREST(t, c.status, map[string]any{"detail": "erro"}, &corpo, &query)
			_, err := pollsClienteREST(t, ts).VotarEnquete(context.Background(), syncz.EntradaVotarEnquete{
				InstanciaID: "inst-1", PollID: "poll-1", Opcoes: []string{"a"},
			})
			if !errors.Is(err, c.erro) {
				t.Fatalf("esperava %v, obteve: %v", c.erro, err)
			}
		})
	}
}

// VotePoll do mock gRPC: devolve as opções recebidas, ou o erro pelo poll_id.
func (m *mockPollsServer) VotePoll(_ context.Context, req *synczv1.VotePollRequest) (*synczv1.VotePollResponse, error) {
	m.votoRecebido = req
	switch req.GetPollId() {
	case "poll-invalida":
		return nil, status.Error(codes.InvalidArgument, "options invalida")
	case "poll-inexistente":
		return nil, status.Error(codes.NotFound, "enquete nao encontrada")
	case "poll-encerrada":
		return nil, status.Error(codes.FailedPrecondition, "enquete encerrada")
	}
	return &synczv1.VotePollResponse{PollId: req.GetPollId(), Options: req.GetOptions()}, nil
}

func TestVotarEnquete_GRPC_Voto(t *testing.T) {
	cli, mockSrv := pollsClienteGRPC(t)

	voto, err := cli.VotarEnquete(context.Background(), syncz.EntradaVotarEnquete{
		InstanciaID: "inst-1", PollID: "poll-1", Opcoes: []string{"chocolate"},
	})
	if err != nil {
		t.Fatalf("VotarEnquete falhou: %v", err)
	}
	if voto.PollID != "poll-1" || !reflect.DeepEqual(voto.Opcoes, []string{"chocolate"}) {
		t.Errorf("voto inesperado: %+v", voto)
	}
	req := mockSrv.votoRecebido
	if req.GetInstanceId() != "inst-1" || !reflect.DeepEqual(req.GetOptions(), []string{"chocolate"}) || !req.GetWait() {
		t.Errorf("requisição inesperada: %+v", req)
	}
}

func TestVotarEnquete_GRPC_Retirada(t *testing.T) {
	cli, mockSrv := pollsClienteGRPC(t)

	voto, err := cli.VotarEnquete(context.Background(), syncz.EntradaVotarEnquete{
		InstanciaID: "inst-1", PollID: "poll-1", EsperaSincrona: syncz.EsperaSincrona{Assincrono: true},
	})
	if err != nil {
		t.Fatalf("VotarEnquete falhou: %v", err)
	}
	if len(voto.Opcoes) != 0 || len(mockSrv.votoRecebido.GetOptions()) != 0 || mockSrv.votoRecebido.GetWait() {
		t.Errorf("retirada inesperada: %+v / %+v", voto, mockSrv.votoRecebido)
	}
}

func TestVotarEnquete_GRPC_Erros(t *testing.T) {
	casos := map[string]error{
		"poll-invalida":    syncz.ErrInvalido,
		"poll-inexistente": syncz.ErrNaoEncontrado,
		"poll-encerrada":   syncz.ErrConflito,
	}
	cli, _ := pollsClienteGRPC(t)
	for pollID, esperado := range casos {
		t.Run(pollID, func(t *testing.T) {
			_, err := cli.VotarEnquete(context.Background(), syncz.EntradaVotarEnquete{InstanciaID: "inst-1", PollID: pollID})
			if !errors.Is(err, esperado) {
				t.Fatalf("esperava %v, obteve: %v", esperado, err)
			}
		})
	}
}

func enqueteSandbox(t *testing.T, sbx syncz.SandboxCliente) syncz.Enquete {
	t.Helper()
	enq, err := sbx.CriarEnquete(context.Background(), syncz.EntradaCriarEnquete{
		InstanciaID: "inst-1", GrupoJID: "123@g.us", Pergunta: "Qual?", Opcoes: []string{"a", "b"},
	})
	if err != nil {
		t.Fatalf("CriarEnquete falhou: %v", err)
	}
	return enq
}

func TestVotarEnquete_Sandbox_VotoERetirada(t *testing.T) {
	ctx := context.Background()
	sbx := syncz.NovoSandbox()
	enq := enqueteSandbox(t, sbx)

	voto, err := sbx.VotarEnquete(ctx, syncz.EntradaVotarEnquete{InstanciaID: "inst-1", PollID: enq.PollID, Opcoes: []string{"b"}})
	if err != nil || !reflect.DeepEqual(voto.Opcoes, []string{"b"}) || voto.PollID != enq.PollID {
		t.Fatalf("voto inesperado: %+v, %v", voto, err)
	}
	res, _ := sbx.ResultadoEnquete(ctx, "inst-1", enq.PollID)
	if res.TotalVotantes != 1 || res.Opcoes[0].Votos != 0 || res.Opcoes[1].Votos != 1 || len(res.Opcoes[1].Votantes) != 1 {
		t.Errorf("resultado deveria mostrar o voto em b: %+v", res)
	}

	if _, err := sbx.VotarEnquete(ctx, syncz.EntradaVotarEnquete{InstanciaID: "inst-1", PollID: enq.PollID}); err != nil {
		t.Fatalf("retirar voto falhou: %v", err)
	}
	res, _ = sbx.ResultadoEnquete(ctx, "inst-1", enq.PollID)
	if res.TotalVotantes != 0 || res.Opcoes[1].Votos != 0 {
		t.Errorf("voto deveria ter sido retirado: %+v", res)
	}
}

func TestVotarEnquete_Sandbox_Erros(t *testing.T) {
	ctx := context.Background()
	sbx := syncz.NovoSandbox()
	enq := enqueteSandbox(t, sbx)
	encerrada := enqueteSandbox(t, sbx)
	if _, err := sbx.EncerrarEnquete(ctx, "inst-1", encerrada.PollID); err != nil {
		t.Fatalf("EncerrarEnquete falhou: %v", err)
	}

	casos := map[string]struct {
		pollID string
		opcoes []string
		erro   error
	}{
		"inexistente":     {"poll-x", []string{"a"}, syncz.ErrNaoEncontrado},
		"encerrada":       {encerrada.PollID, []string{"a"}, syncz.ErrConflito},
		"opcao invalida":  {enq.PollID, []string{"z"}, syncz.ErrInvalido},
		"opcao repetida":  {enq.PollID, []string{"a", "a"}, syncz.ErrInvalido},
		"acima do limite": {enq.PollID, []string{"a", "b"}, syncz.ErrInvalido},
	}
	for nome, c := range casos {
		t.Run(nome, func(t *testing.T) {
			_, err := sbx.VotarEnquete(ctx, syncz.EntradaVotarEnquete{InstanciaID: "inst-1", PollID: c.pollID, Opcoes: c.opcoes})
			if !errors.Is(err, c.erro) {
				t.Fatalf("esperava %v, obteve: %v", c.erro, err)
			}
		})
	}
}
