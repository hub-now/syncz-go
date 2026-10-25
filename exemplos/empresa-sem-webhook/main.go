// Command empresa-sem-webhook mostra a entrada da empresa com só a URL base e a chave de API (docs/AUTH.md): consultar os
// eventos de todas as instâncias sem webhook, retomando pelo cursor, e cadastrar o próprio webhook. Roda contra o Dublê
// (syncz.NovoSandbox); com o gateway real, troque por syncz.Novo(syncz.Opcoes{BaseURL: ..., ChaveAPI: ...}).
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	syncz "github.com/hub-now/syncz-go"
)

func main() {
	ctx := context.Background()
	c := syncz.NovoSandbox()
	defer func() { _ = c.Fechar() }()
	c.InjetarEventoEmpresa(syncz.Evento{ID: "ev-1", Tipo: syncz.TipoMessageReceived, InstanciaID: "loja-centro"})
	c.InjetarEventoEmpresa(syncz.Evento{ID: "ev-2", Tipo: syncz.TipoMessageReceived, InstanciaID: "loja-sul"})

	// 1. Sem webhook: leia as últimas 2h e guarde o cursor.
	consulta, err := c.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{Desde: 2 * time.Hour})
	if err != nil {
		log.Fatal(err)
	}
	for _, e := range consulta.Eventos {
		fmt.Printf("evento %d: %s da instancia %s\n", e.Cursor, e.Evento.Tipo, e.Evento.InstanciaID)
	}

	// 2. Na próxima rodada, continue do cursor: só vem o que é novo, sem repetir (TemMais = lote cheio, chame de novo).
	c.InjetarEventoEmpresa(syncz.Evento{ID: "ev-3", Tipo: syncz.TipoMessageReceived, InstanciaID: "loja-centro"})
	consulta, err = c.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{Apos: consulta.UltimoCursor})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("novos depois do cursor: %d (tem mais: %v)\n", len(consulta.Eventos), consulta.TemMais)

	// 3. Quer receber por webhook? Cadastre o seu: o segredo volta UMA vez -- guarde para o VerificarWebhook.
	w, err := c.DefinirWebhook(ctx, "https://erp.exemplo.com/syncz", nil)
	if err != nil {
		log.Fatal(err)
	}
	if w, err = c.TestarWebhook(ctx); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("webhook %s: %s (teste respondeu %d)\n", w.URL, w.Status, w.StatusTeste)
}
