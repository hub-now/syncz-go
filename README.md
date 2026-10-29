# syncz-go — SDK Go do sync-zap

Cliente Go do gateway `sync-zap`. A mesma interface fala **REST** e **gRPC**: você
escolhe o transporte por opção, não por reescrever o código.

> **Conteúdo gerado.** A fonte deste repositório é `sdk/go/` do gateway, espelhado
> a cada tag. Edição feita aqui desaparece na próxima publicação — veja
> `PUBLICACAO.md`.

## Instalação

```bash
go get github.com/hub-now/syncz-go@v0.4.1
```

`v0.x` significa interface ainda em movimento: pode quebrar em uma minor. Fixe a
versão no seu `go.mod`.

## Primeira mensagem

```go
package main

import (
	"context"
	"log"
	"os"

	syncz "github.com/hub-now/syncz-go"
)

func main() {
	c, err := syncz.Novo(syncz.Opcoes{
		BaseURL:  os.Getenv("SYNCZ_BASE_URL"),
		ChaveAPI: os.Getenv("SYNCZ_API_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Fechar() }()

	r, err := c.Enviar(context.Background(), syncz.EntradaEnvio{
		InstanciaID:       os.Getenv("SYNCZ_INSTANCIA"),
		Para:              "5511999999999",
		Texto:             "primeira mensagem pelo SDK",
		ChaveIdempotencia: "primeira-1",
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Println("enviada:", r.MensagemID)
}
```

O mesmo arquivo, executável, está em [`exemplos/primeira-mensagem`](exemplos/primeira-mensagem).

## Mídia recebida

Quando chega uma mensagem com anexo, o `message.received` traz `Midia` e, logo depois, o gateway baixa o arquivo e
manda `media.ready` (`syncz.PayloadMediaReady`) ou `media.failed` (`syncz.PayloadMediaFailed`). Os dois trazem o
`WAMensagemID` da mensagem de origem: é por ele que você casa a mídia com o `message.received`.

A `URL` do `media.ready` é uma URL de capacidade: vale um download, vence em `ExpiraEm` e é ela mesma a credencial.
Baixe em streaming e ponha o seu teto:

```go
corpo, err := c.BaixarMidia(ctx, p.URL) // 404 (vencida ou já usada) = syncz.ErrNaoEncontrado
if err != nil {
	return err
}
defer corpo.Close()
dados, err := io.ReadAll(io.LimitReader(corpo, 10<<20))
```

Só no transporte REST (no gRPC devolve `syncz.ErrTransporteNaoSuporta`). No Dublê, `InjetarMidia(url, dados)` faz a
URL servir os bytes uma vez. O webhook só recebe `media.ready` se ele estiver no `event_types` do endpoint (ou se o
filtro estiver vazio).

## Empresa com só a URL base e a chave

O admin entrega à empresa só duas coisas: a **URL base** do gateway e a **chave de API**. Com elas a empresa lê os
eventos de todas as suas instâncias sem ter webhook, e, se quiser, cadastra o próprio webhook. Só no transporte REST (no
gRPC esses métodos devolvem `syncz.ErrTransporteNaoSuporta`).

```go
c, _ := syncz.Novo(syncz.Opcoes{BaseURL: "https://gateway.exemplo.com", ChaveAPI: chave})

// Sem webhook: leia as últimas 2h (20m, 30m, 1h, 2h ou 24h; sem nada = 24h) e guarde o cursor.
r, err := c.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{Desde: 2 * time.Hour})
for _, e := range r.Eventos {
	processar(e.Cursor, e.Evento) // Cursor cresce sempre
}
// Próxima rodada: continue do cursor, sem repetir. TemMais = lote cheio (1000), chame de novo.
r, err = c.ConsultarEventos(ctx, syncz.OpcoesConsultaEventos{Apos: r.UltimoCursor})

// Com webhook próprio: o Segredo volta UMA vez (guarde para o VerificarWebhook).
w, err := c.DefinirWebhook(ctx, "https://erp.exemplo.com/syncz", nil) // nil = todos os tipos
w, err = c.TestarWebhook(ctx)                                            // 2xx deixa Status = "active"
```

Também há `ConsultarWebhook` (sem o segredo), `ApagarWebhook` (os eventos seguintes ficam só na consulta) e
`RotacionarSegredoWebhook` (segredo novo uma vez; o anterior vale na janela de rotação). Os eventos ficam 24h no gateway.
No Dublê, `InjetarEventoEmpresa(ev)` põe um evento na consulta. Exemplo executável em
[`exemplos/empresa-sem-webhook`](exemplos/empresa-sem-webhook).

## Reconectar o WhatsApp no seu sistema

Quando o titular remove o aparelho, a instância vai a `logged_out` e o gateway emite `instance.relink_required`. Quem avisa
o titular é a sua empresa; o gateway nunca contata o titular. Para religar a mesma instância, escolha uma das duas formas:

- **Link longo no seu sistema:** monte o token com o seu segredo e ponha o link longo na sua tela. A cada clique,
  `HandlerReconectar` gera um link novo de 10 min na API e responde 302 para ele.

```go
token := syncz.AssinarReconexao(segredo, instanciaID) // o segredo fica só no seu servidor
http.Handle("/reconectar", syncz.HandlerReconectar(c, syncz.ResolverAssinado(segredo, "t")))
// Mande ao titular: https://seu.sistema/reconectar?t=<token>
```

- **Tela própria:** `c.ParearComTelefone(ctx, instanciaID, telefone)` rearma a instância e devolve o QR e o `PairCode`
  (código de 8 dígitos). Acompanhe com `AcompanharPareamento` (só REST). O telefone tem de ser o mesmo número de antes.

## gRPC com TLS (gateway público)

O gateway público publica gRPC na porta 443, com TLS. Endereço terminado em
`:443` liga TLS sozinho, com o certificado verificado contra as raízes do sistema
(a partir da `v0.1.3`):

```go
c, err := syncz.Novo(syncz.Opcoes{
	EnderecoGRPC: "gateway.exemplo.com:443", // :443 => TLS automático
	ChaveAPI:     os.Getenv("SYNCZ_API_KEY"),
})
```

| Situação | Opção |
|---|---|
| `host:443` | nada: TLS automático, raízes do sistema |
| TLS em outra porta, CA própria ou `ServerName` | `TLSGRPC: &tls.Config{RootCAs: pool}` |
| texto puro (h2c) mesmo na 443 | `GRPCSemTLS: true` |
| `localhost:50051`, `gateway:50051` (dev, rede interna) | nada: texto puro, como na `v0.1.2` |

`TLSGRPC` e `GRPCSemTLS` são mutuamente exclusivos. A chave vai no metadata
`authorization` de cada chamada nos dois transportes. Fora da sua máquina ou da
rede interna, use TLS.

## O que vem junto

| | |
|---|---|
| `syncz.Novo` | cliente REST (padrão) ou gRPC, mesma interface |
| `syncz.NovoSandbox` | gateway falso em memória — testa sua integração sem rede e sem instância |
| `syncz.VerificarWebhook` | valida a assinatura HMAC dos webhooks recebidos |
| `synczv1` | stubs gRPC gerados do `.proto` do gateway |

## Contrato e compatibilidade

O contrato é do gateway, não do SDK: `GET /api/v1/contract` devolve o hash do
contrato que a sua instância está servindo, a versão semântica dele
(`contract_version`) e as versões ainda atendidas (`supported_versions`).
Divergiu do que este SDK espera? Atualize o SDK — o gateway decide as regras.

`contract_version` não é o `v1` do caminho: aquele é a versão do transporte e
não muda. Campo ou rpc novo sobe a *minor*; remoção, renomeação ou troca de
tipo sobe a *major*. É por ela que dá para saber se a divergência que o hash
acusou exige trabalho seu ou não.

Bug, dúvida ou pedido de capacidade: abra no repositório do gateway. Aqui não há
código para consertar, só o resultado da publicação.
