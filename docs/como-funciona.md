# Como funciona o SDK

> Explicação de referência do SDK Go do `sync-zap` (`syncz-go`): o que é, como é
> gerado, como é publicado e o que garante que ele nunca minta sobre o contrato.
> Para a jornada prática "do nada à primeira mensagem", veja
> `docs/do-zero-a-primeira-mensagem.md` (nesta mesma pasta). Os caminhos citados
> fora de `sdk/go/` (proto, internal, scripts, workflows, specs) são do
> repositório do servidor, o `sync-zap`; este doc é publicado junto com o SDK.

## Em uma frase

O gateway manda no contrato; o SDK é um **espelho gerado e versionado** desse
contrato; e há portões automáticos no CI garantindo que o espelho não diverge.

---

## 1. O que é

Uma **biblioteca Go** — módulo `github.com/hub-now/syncz-go` — que um
cliente ("tenant") instala no projeto dele para falar com o gateway `sync-zap`
sem montar requisições na mão.

Em vez de:

```go
http.Post("https://.../api/v1/instances/xxx/messages", "application/json", corpoJSON)
```

o cliente escreve:

```go
c, _ := syncz.Novo(syncz.Opcoes{BaseURL: "https://...", ChaveAPI: "..."})
c.Enviar(ctx, syncz.EntradaEnvio{
    InstanciaID:       "...",
    Para:              "5511999999999",
    Texto:             "primeira mensagem pelo SDK",
    ChaveIdempotencia: "primeira-1",
})
```

O exemplo executável fica em `sdk/go/exemplos/primeira-mensagem`.

---

## 2. A ideia central: o contrato mora no gateway

O que define **o que o gateway aceita e devolve** é um único arquivo:
`proto/syncz/v1/syncz.proto` (dentro do `sync-zap`). Ele é a **fonte única da
verdade** — junto com `docs/current/api-contract.openapi.yaml` para o lado REST.

O SDK **não inventa nada**. Os stubs em `sdk/go/synczv1/*.pb.go` são *gerados*
a partir do `.proto`:

```
proto/syncz/v1/syncz.proto  ──( buf generate )──►  sdk/go/synczv1/*.pb.go              (o SDK; módulo syncz-go)
       (a verdade)                          └──►  internal/contratos/synczv1/*.pb.go  (a cópia do servidor)
                                                  (as duas geradas; nunca editar à mão)
```

São **duas cópias do mesmo `.proto`**, geradas juntas pelo mesmo `buf generate`
(DM-15, 15/10/2026): o servidor usa a dele, em `internal/contratos/synczv1`, e não
importa o SDK. Assim o SDK é só de quem consome a API, e as duas nunca entram no
mesmo binário (o protobuf não aceita o mesmo `.proto` registrado duas vezes).

Por cima dos stubs há uma casca fina, escrita à mão, que deixa a API agradável
(`syncz.Novo`, `c.Enviar`, `c.CriarInstancia`, …). São os arquivos
`sdk/go/syncz.go`, `sdk/go/rest.go`, `sdk/go/grpc.go`, `sdk/go/opcoes.go`.

Decisão de design: **D-13.1.1** (`docs/specs/fase-13-1-plug-and-play-com-prova.md`)
— "o gateway continua dono do contrato; o SDK vira artefato".

---

## 3. REST ou gRPC: a mesma interface, você escolhe o transporte

```go
syncz.Novo(syncz.Opcoes{ BaseURL:      "https://..." })   // fala REST (padrão)
syncz.Novo(syncz.Opcoes{ EnderecoGRPC: "host:porta"   })  // fala gRPC
```

Os dois devolvem o mesmo tipo `Cliente` (a `interface` em `sdk/go/syncz.go`), então
o resto do seu código não muda. `BaseURL` e `EnderecoGRPC` são **mutuamente
exclusivos**; não passar nenhum dá erro (`sdk/go/opcoes.go`).

O gRPC existe principalmente pelo streaming de eventos (`StreamEvents`) e pelo
pareamento headless (`StartPairing`).

### gRPC com TLS (gateway público, desde a v0.1.3)

O gRPC público (staging, D-16.2) chega pelo Traefik na **443 com TLS** e segue
em h2c até o edge. O SDK escolhe o transporte em `credencialTransporte`
(`sdk/go/grpc.go`):

```go
// gateway público: :443 liga TLS sozinho, certificado verificado com as raízes do sistema
syncz.Novo(syncz.Opcoes{EnderecoGRPC: "qaccaewnj98e8c3brnq1r8j9.89.167.0.87.sslip.io:443", ChaveAPI: chave})

// TLS em outra porta ou com CA própria
syncz.Novo(syncz.Opcoes{EnderecoGRPC: "gateway.interno:8443", ChaveAPI: chave,
	TLSGRPC: &tls.Config{RootCAs: pool}})

// texto puro forçado, inclusive na 443
syncz.Novo(syncz.Opcoes{EnderecoGRPC: "proxy-h2c:443", ChaveAPI: chave, GRPCSemTLS: true})
```

| Opções | Transporte |
|---|---|
| `GRPCSemTLS: true` | texto puro (h2c) |
| `TLSGRPC != nil` | TLS com essa config (clonada, piso TLS 1.2) |
| nenhuma, endereço termina em `:443` | TLS, raízes do sistema |
| nenhuma, qualquer outra porta | texto puro, igual à v0.1.2 |

Por que **não** TLS por padrão para todo host não-loopback: compose e k8s usam
`gateway:50051` em h2c desde a v0.1.0, e mudar isso quebraria esses clientes numa
patch. A chave vai por interceptor no metadata `authorization` (não por
`PerRPCCredentials`), então não exige TLS no caso inseguro. Com TLS, um
certificado inválido derruba o handshake antes de qualquer chamada, e a chave não
sai da máquina. Travas: `sdk/go/grpc_transporte_test.go` (escolha) e
`sdk/go/grpc_tls_test.go` (servidor local sob TLS autoassinado e h2c).

### O que vem junto

| Símbolo | Para que serve |
|---|---|
| `syncz.Novo` | cliente REST (padrão) ou gRPC, mesma interface |
| `syncz.NovoSandbox` | gateway falso em memória — testa sua integração sem rede e sem instância |
| `syncz.VerificarWebhook` | valida a assinatura HMAC dos webhooks recebidos |
| `synczv1` | stubs gRPC gerados do `.proto` do gateway |

---

## 3.1. Cobertura por transporte (fechada na `v0.2.0`, tasks 564-570)

A `docs/operacao/homologacao/matriz-sdk.md` (CA-29) media, tarefa 518, nove lacunas em
`Cliente` frente às rotas REST: cada uma virou uma tarefa isolada (564-570) que
ou fechou o método nos três transportes, ou documentou por que um transporte
fica de fora com `ErrTransporteNaoSuporta` (comparável via `errors.Is`) em vez
de inventar um valor.

| Método | REST | gRPC | Sandbox | Observação |
|---|---|---|---|---|
| `StatusMensagem` (task 565) | sim (`GET .../messages/{message_id}`) | `ErrTransporteNaoSuporta` | sim | o proto não tem RPC equivalente — decisão registrada em `sdk/go/grpc.go` |
| `StatusOperacao` (task 566) | sim (`GET .../operations/{op_id}`) | `ErrTransporteNaoSuporta` | sim | RPCs de grupo no proto sempre devolvem o tipo final direto, sem `op_id` assíncrono — não há caminho "accepted" a consultar em gRPC |
| `CriarEnquete` / `ResultadoEnquete` / `EncerrarEnquete` (task 567) | sim (`POST/GET .../polls`, `.../polls/{id}/results`, `.../polls/{id}/close`) | sim (`CreatePoll`/`GetPollResults`/`ClosePoll` existem no proto) | sim | único trio das 7 tasks com RPC gRPC nativo — sem `ErrTransporteNaoSuporta` |
| `VotarEnquete` (task 178) | sim (`POST .../polls/{id}/vote`, corpo `{"options":[...]}`; lista vazia retira o voto) | sim (`VotePoll`) | sim (guarda o voto e o mostra em `ResultadoEnquete`) | 400 `ErrInvalido`, 404 `ErrNaoEncontrado`, 409 `ErrConflito` |
| `CriarEventoGrupo` / `AtualizarEventoGrupo` / `ResponderEventoGrupo` / `CancelarEventoGrupo` (task 568) | sim (`POST/PATCH .../group-events`, `.../group-events/{id}/cancel`, `.../group-events/{id}/response`) | sim (`CreateGroupEvent`/`UpdateGroupEvent`/`CancelGroupEvent`/`SendEventResponse` no proto) | sim | também com RPC gRPC nativo, mesmo critério das enquetes |
| `AcompanharEventos` (task 569) | sim — interface `ClienteEventosStream` (`GET /api/v1/events/stream`, SSE) | não tem stream equivalente por asserção de tipo; o gRPC tem RPC próprio de forma diferente (`SyncZap_StreamEventsServer`, filtro por múltiplas instâncias/tipos) | não | descubra o suporte com `if streamer, ok := cli.(syncz.ClienteEventosStream); ok { ... }`; hoje o servidor só faz *catch-up* (despeja o que já tem com `seq >= fromSeq` e fecha) — não é stream ao vivo |
| `AtualizarEstadoDesejado` (task 570) | sim (`PATCH /api/v1/instances/{id}`) | `ErrTransporteNaoSuporta` | sim | proto não tem `UpdateInstance`/`SetDesiredState` |
| `AcompanharPareamento` (task 564, pré-existente na `v0.1.x`) | sim — interface `ClientePareamentoStream` (`GET .../pairing/stream`, SSE) | sem RPC de streaming equivalente | não | mesmo padrão de asserção de tipo de `AcompanharEventos`; complementa o polling de `Cliente.EstadoPareamento` |
| `SolicitarConsentimento` (task 589, `v0.3.0`, CA-16/CA-13) | sim (`POST .../consent/request`) | sim (`RequestConsent`, task 588) | sim | pede ao titular, por DM pelo número pareado, que autorize a instância — alternativa a desenhar tela própria |
| `Desparear` (task 589, `v0.3.0`, CA-16/CA-20) | sim (`POST .../unpair`) | sim (`UnpairInstance`, task 588) | sim | desfaz o pareamento sem apagar a instância; o aparelho some de "Aparelhos conectados" no celular |
| `RegistrarAceite.Evidencia` (task 589, `v0.3.0`, CA-16/CA-06) | sim (campo `evidence` no corpo) | sim (`GrantConsentRequest.evidence`, `google.protobuf.Struct`, task 588) | sim | objeto JSON livre ≤ 4KiB que só o caminho `tenant` aceita — prova de como a tela própria obteve o aceite |
| `ObterInstancia.Consentimento`/`.PareadoEm` (task 589, `v0.3.0`, CA-16/CA-08) | sim | sim | sim | bloco `consent` (status/origin/granted_by/...) e `paired_at` da instância, desde a task 583 |
| `ConsultarEventos` e `DefinirWebhook`/`ConsultarWebhook`/`ApagarWebhook`/`TestarWebhook`/`RotacionarSegredoWebhook` (tarefa 160) | sim (`GET /api/v1/events/stream` sem `instance_id`; `PUT/GET/DELETE /api/v1/webhook`, `POST .../test`, `POST .../rotate-secret`) | `ErrTransporteNaoSuporta` | sim | a empresa entra só com a URL base e a chave: consulta sem webhook pelo cursor, ou cadastra o próprio webhook |
| `ObterContratoTenant.PreConsentimento` (task 589, `v0.3.0`, CA-16) | sim (`GET /api/v1/contract`, campo `pre_consent`) | `ClienteContratoTenant` não implementado (descoberta de contrato é HTTP-only por decisão de arquitetura, T-22) | não | descubra o suporte com `if ct, ok := cli.(syncz.ClienteContratoTenant); ok { ... }`, mesmo padrão de `ClienteEventosStream` |

Regra de desenho consistente nas 7 tasks: quando o `.proto` **não tem** RPC
equivalente, `clienteGRPC` entra na interface `Cliente` normal e devolve
`ErrTransporteNaoSuporta` de forma explícita (nunca um valor inventado, nunca
pânico). Quando o `.proto` **tem** RPC equivalente, `clienteGRPC` implementa de
verdade. Quando **nenhum transporte tem stream nativo comparável em forma**
(pareamento e eventos), o método fica **fora** de `Cliente` e vira uma
interface adicional (`ClientePareamentoStream`, `ClienteEventosStream`)
descoberta por asserção de tipo — adicionar o RPC no `.proto` é decisão de
contrato cross-repo (server + SDK), fora do escopo de uma tarefa isolada de
SDK client. Ver `sdk/go/message_status_test.go`, `operation_status_test.go`,
`desired_state_test.go` (casos `*_GRPCNaoSuportado`) e `polls_test.go`,
`group_events_test.go` (casos `*_GRPC`, provando o caminho nativo) para os
testes que travam essa matriz.

### O `seq` dos eventos: cresce, nunca se repete, pode saltar

Cada evento de uma instância traz um `seq` (`AcompanharEventos`, o
`StreamEvents` do gRPC, o webhook). A regra que o cliente pode usar:

- o `seq` de uma instância **sempre cresce** e **nunca se repete**: retome o
  stream pelo último `seq` que você processou (`from_seq`) e guarde-o como
  ponto de retomada;
- o `seq` **pode saltar**: depois de uma restauração do banco a partir de um
  backup, o servidor leva o contador ao instante atual em milissegundos
  (spec `restaurar-do-backup`, comando `cmd/pos-restauracao`), para que
  nenhum evento novo repita um número que você já tem. Um salto **não** é
  evento perdido; não trate "faltou o `seq` N+1" como erro.

Os eventos emitidos entre o instante do backup e a restauração não existem
mais no servidor; a operação avisa as empresas do instante restaurado.

---

## 4. Como o SDK é publicado

O código-fonte do SDK vive **dentro do `sync-zap`**, em `sdk/go/`. Mas o
`sync-zap` é um repositório **privado** — se o cliente fizesse `go get` apontando
para cá, levaria o código do gateway junto.

Solução: **espelhamento** para um segundo repositório, **público e sem nada do
gateway**: `hub-now/syncz-go`. O módulo declara esse endereço como seu
próprio nome (`sdk/go/go.mod`: `module github.com/hub-now/syncz-go`), então
`go get` do tenant resolve direto para lá.

Até a v0.3.0 o módulo era `github.com/quanturisai-ai/syncz-go` (conta pessoal). Desde a
v0.4.0 (17/10/2026, migração para a HubNow) é `github.com/hub-now/syncz-go`; o
repositório antigo fica com as versões já publicadas e marcado como `Deprecated`.

A v0.4.1 (18/10/2026) corrige o Dublê: `CriarGrupo` gera JID de grupo único entre Dublês (semente sorteada por
`NovoSandbox`, mesmo formato `<dígitos>@g.us`). Até a v0.4.0 o 1º grupo de todo Dublê tinha o mesmo JID, e dois
pacotes de teste no mesmo banco colidiam. O módulo passa a pedir Go 1.27.2 (`x/net` v0.60.0, govulncheck limpo).

A v0.4.3 (25/10/2026, patch aditivo) corrige o `EnviarMidia` pelo REST: a parte do arquivo passa a levar
`Content-Type` = `EntradaEnvioMidia.MimeType` (como o gRPC já fazia). Sem `MimeType`, segue `application/octet-stream`.
Antes, áudio `audio/ogg; codecs=opus` chegava ao gateway como octet-stream e virava documento, não nota de voz (PTT).

Próxima versão (tarefa 160, spec `empresa-sem-webhook`; ainda sem tag): mudança **aditiva**, nenhum método existente muda.
`Cliente` ganha `ConsultarEventos` (eventos de todas as instâncias da empresa por `GET /api/v1/events/stream` sem
`instance_id`, janela `Desde` ou cursor `Apos`, lote de 1000, `Cursor` = a linha `id:` do SSE) e os métodos do webhook da
empresa pela chave: `DefinirWebhook`, `ConsultarWebhook`, `ApagarWebhook`, `TestarWebhook` e `RotacionarSegredoWebhook`
(`/api/v1/webhook*`). Só REST; no gRPC devolvem `ErrTransporteNaoSuporta`. O Dublê ganha `InjetarEventoEmpresa` e guarda o
webhook em memória. Quem implementa `Cliente` por conta própria (mock) precisa dos 6 métodos novos.

O fluxo, disparado por uma tag `sdk/vX.Y.Z` neste repositório:

```
alguém cria a tag  sdk/v0.2.0  no sync-zap  (git push origin sdk/v0.2.0)
        │
        ▼
dispara  .github/workflows/app-sdk-publish.yml   (on: push tags 'sdk/v*'  |  workflow_dispatch)
        │
        ▼
scripts/publish-sdk.sh:
   1. confere que sdk/go/go.mod declara o mesmo endereço do repositório destino
   2. regenera os stubs do .proto e falha se sdk/go/synczv1/ ou internal/contratos/synczv1/
      estiver fora de sincronia
   3. roda a suíte do SDK isolada  (cd sdk/go && go test ./...)
   4. clona o repositório público, apaga tudo menos .git, copia SÓ sdk/go/
   5. escreve PUBLICACAO.md (versão + commit de origem)
   6. commita, cria a tag  vX.Y.Z  (sem o prefixo sdk/) e faz push
        │
        ▼
cliente:  go get github.com/hub-now/syncz-go@v0.4.1
```

Requisitos do job: o secret `SDK_PUBLISH_TOKEN` (um PAT com escrita no
repositório público — o `GITHUB_TOKEN` padrão só alcança este repositório). PAT
fine-grained vale para UM dono: o de `quanturisai-ai` não escreve em `hub-now/syncz-go`.

Ninguém edita o repositório público à mão: o próximo espelho sobrescreve. É
artefato, não fonte. Bug ou pedido de capacidade se abre no gateway.

Decisão de design: **ADR D-13.2 / F13-02**
(`docs/specs/adr-13-02-publicacao-do-sdk-go.md`).

### R-01 da auditoria 389 (tarefa 472) — resolvido

Até 07/09/2026 a `v0.1.0` do repositório público **não tinha sido produzida por
este pipeline** — `scripts/publish-sdk.sh` e o workflow estavam escritos mas
nunca exercitados de ponta a ponta, e o secret `SDK_PUBLISH_TOKEN` nem existia.

Fechado em duas frentes:

1. **Gate contínuo** — o job `dry-run` de `sdk-publish.yml` roda
   `publish-sdk.sh … --dry-run` a cada PR que toca o SDK, o contrato ou a
   maquinaria: regenera stubs, roda a suíte isolada, monta a árvore do espelho
   e imprime o diff — sem push.
2. **Prova de publicação real** — o `SDK_PUBLISH_TOKEN` foi provisionado (PAT
   fine-grained, `Contents: write` só em `quanturisai-ai/syncz-go`) e a tag
   `sdk/v0.1.2` foi empurrada: o pipeline rodou verde, espelhou `sdk/go/`,
   atualizou o `main` do repo público e criou `v0.1.2` lá
   ([run 34173062280](https://github.com/quanturisai-ai/sync-zap/actions/runs/34173062280)).
   `go get github.com/quanturisai-ai/syncz-go@v0.1.2` resolve.

A `v0.1.0` **não foi sobrescrita** — o `sync-mb` a consome pinada; `v0.1.2` é o
primeiro release feito pelo processo (a `v0.1.1` foi um número queimado num
run que falhou por falta de `gh auth setup-git` no runner, já corrigido).

---

## 5. Versão e compatibilidade

`GET /api/v1/contract` (implementado em `internal/modulos/rest/contract.go`)
devolve, para a instância que respondeu:

| Campo | O que é |
|---|---|
| `api_version` | versão do **transporte** — o `v1` do caminho da URL. Não muda. |
| `contract_hash` | `sha256` do `.proto` normalizado (`internal/contratos.Hash`) — impressão digital do contrato servido. |
| `contract_version` | **versão semântica do contrato** (ex.: `1.0.0` → `1.1.0` → …). |
| `supported_versions` | versões de contrato que este gateway ainda atende. |
| `rpcs` / `rest_routes` | contagem de rpc do proto e de rotas REST registradas. |
| `generated_at` | instante em que o hash foi calculado (uma vez, no boot). |

Regra de evolução:

- campo ou rpc **novo** → sobe a *minor* (aditivo, não quebra você);
- **remoção, renomeação ou troca de tipo** → sobe a *major* (você precisa
  atualizar o SDK).

É por `contract_version` — não pelo `contract_hash` sozinho — que dá para saber
se a divergência que o hash acusou exige trabalho seu ou não.

Decisões de design: **D-13.1.4** (versão do contrato é dado, não convenção de
URL) e **D-13.1.5** (depreciação declarada com `Deprecation`/`Sunset` e data
absoluta, janela mínima de 90 dias).

---

## 6. Os portões que mantêm isso honesto

### `.github/workflows/app-sdk.yml` — a cada PR/push que toca `proto/**`, `sdk/go/**` ou `buf.*`

| Job | O que prova |
|---|---|
| **trava anti-divergência** (`sdk-lock`) | `buf generate` na hora tem de bater, byte a byte, com o que está commitado em `sdk/go/synczv1/` e em `internal/contratos/synczv1/`. Alterou o `.proto` e esqueceu de regenerar? reprova e manda rodar `buf generate`. Editou o `.pb.go` à mão? o próximo generate desfaz, e o gate acusa. |
| **suíte isolada** (`sdk-isolado`) | `cd sdk/go && go test ./...` roda o SDK como um tenant o veria: módulo próprio, sem o `go.mod` do gateway em jogo. Se algum código ou teste do SDK depender de `internal/`, quebra aqui — incluindo a varredura de imports em `sdk/go/isolamento_test.go`. |
| **fricção** (`friccao`) | um projeto Go novo, num diretório temporário **fora dos dois repositórios**, chega ao SDK só pela versão publicada e manda a primeira mensagem. Conta as linhas de código próprio (fora `go.mod`, `go.sum` e gerados) e **reprova acima de 50** (D-13.1.2). Sem `cache:` e sem secret, de propósito: o tenant real não tem nenhum dos dois. |

Os dois primeiros provam que o SDK é publicável; o terceiro prova que publicá-lo
serviu para alguma coisa.

### O guia é um teste — `sdk/go/docs/do-zero-a-primeira-mensagem.md`

"Do zero à primeira mensagem" existe em duas formas (curl e SDK) e as duas rodam
no CI contra o sandbox (`tests/arch/guia_test.go`, `bash scripts/rodar-guia.sh`). Documentação que
não é executada apodrece em silêncio (D-13.1.6).

---

## 7. Comandos úteis (raiz do `sync-zap`)

| Comando | O que faz |
|---|---|
| `buf generate` | regenera `sdk/go/synczv1/` e `internal/contratos/synczv1/` a partir do `.proto` (fonte única) |
| `cd sdk/go && go test ./...` | roda a suíte do SDK isolada |
| `bash scripts/medir-friccao.sh` | mede quantas linhas de código próprio um tenant novo precisa (reprova acima de 50) |
| `bash scripts/rodar-guia.sh` | executa o guia "do zero à primeira mensagem" contra o sandbox |

---

## Referências

- `sdk/go/README.md` — instalação e primeira mensagem, do ponto de vista do tenant
- `proto/syncz/v1/syncz.proto` — o contrato (fonte única)
- `docs/specs/fase-13-1-plug-and-play-com-prova.md` — §4 (D-13.1.1 a D-13.1.6), critérios de aceite
- `docs/specs/adr-13-02-publicacao-do-sdk-go.md` — onde e por que o `sdk/go` é publicado
- auditoria 389 (tag `pre-migracao-forge-novo`) — R-01 (pipeline de publicação nunca exercitado) → tarefa 472
- `docs/operacao/homologacao/matriz-sdk.md` — CA-29: cobertura operação a operação, base das tasks 564-570 (CA-33)
- `docs/specs/fase-16-homologacao-staging.md` — CA-33 e T-F6 (esta tarefa, 544)
- `.github/workflows/app-sdk.yml` / `.github/workflows/app-sdk-publish.yml` · `scripts/publish-sdk.sh` · `scripts/medir-friccao.sh`
