# Do zero à primeira mensagem

> **Este guia é um teste.** Cada bloco marcado `guia` abaixo é extraído deste
> arquivo e executado no CI (`.github/workflows/app-guia.yml` → `scripts/rodar-guia.sh`), contra
> um gateway de verdade. Documentação que ninguém executa apodrece em silêncio
> e vira a próxima fonte de "sucesso declarado onde nada aconteceu" — por isso
> ela não vive aqui como prosa, vive como comando. Se um passo deste guia
> quebrar, o job quebra junto: não há como o guia envelhecer sozinho.

Você vai sair do nada e chegar à primeira mensagem. Duas formas, o mesmo
caminho: **curl** (para entender o contrato) e **SDK Go** (para escrever menos).

**O que você NÃO precisa:** clonar o repositório do gateway, rodar `psql`,
editar arquivo de ambiente do sync-zap, ou pedir acesso a qualquer coisa que não
seja uma URL e uma chave.

**O que você precisa:** a URL do seu gateway e, para o passo 0, o token
administrativo de quem o opera.

---

## Passo 0 — provisionar o tenant (uma vez, feito por quem opera o sync-zap)

Não há autoatendimento, **e isso é decisão declarada, não pendência** (dono,
2026-09-07 — `docs/PRD.md`, DD-22): quem cria o tenant é o
operador do gateway, com o token administrativo do processo. Se você é o tenant,
pule este passo — você recebe a chave do passo 1 pronta, e o guia começa de
verdade para você no passo 1.

A resposta já traz uma primeira chave de API: `api_key` aparece **uma vez** e
não é recuperável depois.

```bash guia
: "${SYNCZ_BASE_URL:?defina a URL do gateway}"
: "${AUTH_ADMIN_CHAVE:?token administrativo de quem opera o gateway}"

SYNCZ_TENANT_SLUG="primeiro-tenant-$(date +%s)"

TENANT="$(curl -sSf -X POST "$SYNCZ_BASE_URL/admin/tenants" \
  -H "Authorization: Bearer $AUTH_ADMIN_CHAVE" \
  -H 'Content-Type: application/json' \
  -d "{\"name\":\"Primeiro Tenant\",\"slug\":\"$SYNCZ_TENANT_SLUG\"}")"

SYNCZ_TENANT_ID="$(jq -er '.id' <<<"$TENANT")"
echo "tenant: $SYNCZ_TENANT_ID ($(jq -er '.slug' <<<"$TENANT"))"
```

## Passo 1 — emitir a sua chave de API

A chave nasce com o escopo `api`. Guarde `api_key` inteira: o gateway só
armazena o prefixo e o hash do segredo, então ele não consegue te devolver a
chave depois — perdeu, emite outra.

```bash guia
CHAVE="$(curl -sSf -X POST "$SYNCZ_BASE_URL/admin/tenants/$SYNCZ_TENANT_ID/api-keys" \
  -H "Authorization: Bearer $AUTH_ADMIN_CHAVE" \
  -H 'Content-Type: application/json' \
  -d '{"name":"primeira-integracao"}')"

SYNCZ_API_KEY="$(jq -er '.api_key' <<<"$CHAVE")"
echo "chave emitida: $(jq -er '.prefix' <<<"$CHAVE") (escopos: $(jq -erc '.scopes' <<<"$CHAVE"))"
```

## Passo 2 — confirmar contra qual contrato você está falando

Antes de escrever integração, saiba com o que está falando. `contract_version` é
a versão do **contrato**; o `v1` do caminho é a versão do **transporte** e não
muda. Declare a sua no cabeçalho `X-Contract-Version` em toda requisição: é o
que permite ao gateway avisar você — e só você — quando a sua versão for
desligada.

Se a rota que você usa estiver depreciada, a resposta traz `Deprecation` e
`Sunset` com a data absoluta do desligamento, com no mínimo 90 dias de janela.

```bash guia
CONTRATO="$(curl -sSf "$SYNCZ_BASE_URL/api/v1/contract" \
  -H "Authorization: Bearer $SYNCZ_API_KEY" \
  -H 'X-Contract-Version: 1.0.0')"

SYNCZ_CONTRACT_VERSION="$(jq -er '.contract_version' <<<"$CONTRATO")"
jq -e --arg v "$SYNCZ_CONTRACT_VERSION" '.supported_versions | index($v)' <<<"$CONTRATO" >/dev/null

echo "contrato $SYNCZ_CONTRACT_VERSION (hash $(jq -er '.contract_hash' <<<"$CONTRATO"))"
```

## Passo 3 — registrar o webhook

É por aqui que as mensagens recebidas chegam até você. O `secret` também aparece
**uma vez**: é com ele que você valida a assinatura HMAC de cada entrega
(`syncz.VerificarWebhook`, no SDK). Sem validar assinatura, qualquer um posta no
seu endpoint.

```bash guia
WEBHOOK="$(curl -sSf -X POST "$SYNCZ_BASE_URL/admin/tenants/$SYNCZ_TENANT_ID/webhook" \
  -H "Authorization: Bearer $AUTH_ADMIN_CHAVE" \
  -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com/webhooks/syncz","event_types":["message.received"]}')"

SYNCZ_WEBHOOK_SECRET="$(jq -er '.secret' <<<"$WEBHOOK")"
echo "webhook $(jq -er '.url' <<<"$WEBHOOK") — status $(jq -er '.status' <<<"$WEBHOOK")"
```

## Passo 4 — criar a instância

Uma instância é um número de WhatsApp. Ela nasce inerte em `created`: nada roda
no nó nem consome recursos enquanto ninguém solicitar o pareamento. Ela só sai
daí quando alguém abrir o pareamento e vincular um telefone.

```bash guia
INSTANCIA="$(curl -sSf -X POST "$SYNCZ_BASE_URL/api/v1/instances" \
  -H "Authorization: Bearer $SYNCZ_API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"name":"primeira"}')"

SYNCZ_INSTANCIA="$(jq -er '.id' <<<"$INSTANCIA")"
echo "instancia $SYNCZ_INSTANCIA — status $(jq -er '.status' <<<"$INSTANCIA")"
```

## Passo 5 — parear o telefone

O pareamento opera sob demanda: o QR existe enquanto a tela estiver aberta, com
janela única de 10 minutos (se expirar, retomar é só pedir de novo).

Você não precisa desenhar tela de QR: peça um link do wizard hospedado e mande
para quem tem o telefone na mão. Se preferir desenhar a sua própria jornada,
`GET /api/v1/instances/{id}/pairing` devolve o estado, e
`.../pairing/stream` empurra as atualizações por SSE — sem polling.

```bash guia
LINK="$(curl -sSf -X POST "$SYNCZ_BASE_URL/api/v1/instances/$SYNCZ_INSTANCIA/wizard-link" \
  -H "Authorization: Bearer $SYNCZ_API_KEY" \
  -H 'Content-Type: application/json' -d '{}')"

echo "abra e escaneie: $SYNCZ_BASE_URL$(jq -er '.url' <<<"$LINK") (expira em $(jq -er '.expires_at' <<<"$LINK"))"

PAREAMENTO="$(curl -sSf "$SYNCZ_BASE_URL/api/v1/instances/$SYNCZ_INSTANCIA/pairing" \
  -H "Authorization: Bearer $SYNCZ_API_KEY")"
echo "pareada? $(jq -er '.paired' <<<"$PAREAMENTO")"
```

## Passo 6 — a primeira mensagem

Com o telefone pareado, isto responde `202 Accepted` e um `message_id`.

`Idempotency-Key` não é opcional na prática: sem ela, um retry de rede vira
mensagem duplicada no telefone de alguém.

```bash guia
ENVIO="$(curl -sS -o /tmp/guia-envio.json -w '%{http_code}' \
  -X POST "$SYNCZ_BASE_URL/api/v1/instances/$SYNCZ_INSTANCIA/messages" \
  -H "Authorization: Bearer $SYNCZ_API_KEY" \
  -H 'Content-Type: application/json' \
  -H 'X-Contract-Version: 1.0.0' \
  -H "Idempotency-Key: primeira-$(date +%s)" \
  -d '{"to":{"phone":"5511999999999"},"type":"text","text":{"body":"primeira mensagem"}}')"

# Ramifique pelo `type`, nunca pelo `detail`: `type` e o campo maquina-legivel
# e estavel; `detail` e texto humano. E a diferenca entre "pareie o telefone" e
# "troque a chave e reenvie" -- que, num 409 de instancia nao conectada,
# duplicaria a mensagem assim que ela conectasse.
case "$ENVIO" in
  202) echo "enviada: $(jq -er '.message_id' < /tmp/guia-envio.json)" ;;
  409)
    TIPO="$(jq -er '.type' < /tmp/guia-envio.json)"
    case "$TIPO" in
      */instancia_nao_conectada) echo "ainda sem telefone pareado — pareie e reenvie a MESMA Idempotency-Key" ;;
      */conflito_idempotencia)   echo "essa Idempotency-Key ja foi usada com outro conteudo — gere uma nova" ;;
      *) echo "409 com type inesperado ($TIPO): $(cat /tmp/guia-envio.json)" >&2; exit 1 ;;
    esac
    ;;
  *)   echo "resposta inesperada ($ENVIO): $(cat /tmp/guia-envio.json)" >&2; exit 1 ;;
esac
```

> **No CI não há telefone para escanear o QR**, então este passo termina em
> `409` com `type` = `instancia_nao_conectada` — e o guia trata os desfechos
> porque todos são o contrato. Até 2026-09-07 esse 409 saía com o `type` de
> conflito de idempotência (INS-004), e o guia precisava ler `.detail` para
> distinguir; a task 468 deu à condição o `type` que ela merecia. O
> envio completo, ponta a ponta, é o que a forma SDK prova logo abaixo contra o
> sandbox.

---

## A mesma coisa, com o SDK

O SDK é um módulo público: `go get`, sem token e sem acesso ao repositório do
gateway. A mesma interface fala REST e gRPC — você troca o transporte por opção,
não reescrevendo código.

```go guia:gateway
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

	ctx := context.Background()
	inst, err := c.CriarInstancia(ctx, syncz.EntradaCriarInstancia{Nome: "primeira-sdk"})
	if err != nil {
		log.Fatal(err)
	}

	link, err := c.NovoLinkWizard(ctx, inst.ID)
	if err != nil {
		log.Fatal(err)
	}
	log.Println("instancia:", inst.ID, "status:", inst.Status, "— escaneie:", link.URL)

	// Sem telefone pareado o gateway recusa, e recusar é o comportamento certo.
	_, err = c.Enviar(ctx, syncz.EntradaEnvio{
		InstanciaID:       inst.ID,
		Para:              "5511999999999",
		Texto:             "primeira mensagem",
		ChaveIdempotencia: "primeira-sdk-1",
	})
	log.Println("envio sem pareamento:", err)
}
```

## Sem telefone à mão? O sandbox

`syncz.NovoSandbox()` é o gateway falso em memória: mesma interface, sem rede,
sem instância e sem QR. É como você testa a sua integração no seu CI — e é onde
este guia prova o envio completo.

```go guia:sandbox
package main

import (
	"context"
	"log"

	syncz "github.com/hub-now/syncz-go"
)

func main() {
	ctx := context.Background()
	c := syncz.NovoSandbox()
	defer func() { _ = c.Fechar() }()

	inst, err := c.CriarInstancia(ctx, syncz.EntradaCriarInstancia{Nome: "primeira"})
	if err != nil {
		log.Fatal(err)
	}
	c.InjetarPareamento(inst.ID, "5511999999999")

	r, err := c.Enviar(ctx, syncz.EntradaEnvio{
		InstanciaID:       inst.ID,
		Para:              "5511999999999",
		Texto:             "primeira mensagem",
		ChaveIdempotencia: "primeira-1",
	})
	if err != nil {
		log.Fatal(err)
	}
	log.Println("enviada:", r.MensagemID)
}
```

---

## Onboarding transparente em cinco chamadas

O passo 5 acima ("parear o telefone") te leva pelo wizard hospedado do
sync-zap — QR e tela de consentimento com a marca do sync-zap. Se você quer
sua própria marca, seu próprio layout, ou uma jornada embutida no seu app,
existe um segundo caminho: você desenha a tela, o sync-zap só fornece os
dados e registra o aceite. Cinco chamadas, do zero ao `connected`:

1. **Criar a instância** — `CriarInstancia`, igual ao passo 4 acima.
2. **Receber o QR** — por webhook `instance.pairing_qr` (`DecodificarEvento`
   no SDK) ou acompanhando `AcompanharPareamento`/`EstadoPareamento`; sem
   polling manual.
3. **Mostrar o QR** — na sua própria tela, com sua marca.
4. **Buscar o `Contrato`** — `Contrato(ctx, instanciaID)` devolve os termos e
   escopos que o sync-zap exige; você monta a tela de autorização unindo
   isso com as permissões do seu próprio produto.
5. **Registrar o aceite, de duas formas:**
   - `RegistrarAceite` com `Evidencia` (um objeto JSON livre ≤ 4KiB — a
     prova de como você obteve o aceite: id da sessão, método usado, etc.),
     se o titular aceitou na sua própria tela; ou
   - `SolicitarConsentimento`, se preferir pedir a autorização por DM no
     WhatsApp do próprio titular, sem desenhar tela nenhuma — o sync-zap
     manda a mensagem com um link que abre a tela de aceite.

A instância fica em `awaiting_consent` entre os passos 2 e 5 — sem prazo:
ela pode ficar pareada, aguardando autorização, pelo tempo que você quiser.
Se o seu tenant tiver pré-consentimento ligado (`PATCH /admin/tenants/{id}`,
`pre_consent_send`/`pre_consent_receive`), a instância já pode enviar e/ou
receber mensagens **antes** do passo 5, sob um contrato provisório com
escopos restritos (nunca grupo não gerenciado, nunca escopos opcionais).

Exemplo compilável, rodando contra o sandbox (sem telefone nem servidor):
[`sdk/go/exemplos/onboarding_transparente/main.go`](../exemplos/onboarding_transparente/main.go).
`go run .` termina com exit 0 e imprime a sequência de rótulos `pairing,
paired, awaiting_consent, connected` conforme o fluxo avança.

No repositório do servidor (`sync-zap`), ver as specs `docs/specs/2026-10-sessoes-e-pareamento.md` e `docs/specs/2026-10-contrato-e-consentimento.md` para o fluxo completo
(hospedado vs. transparente, pré-consentimento, desparear) e
`docs/operacao/homologacao/matriz-sdk.md` para a cobertura por transporte de cada
método novo.

---

## E depois

| Você quer | Onde está |
|---|---|
| Validar a assinatura dos webhooks | `syncz.VerificarWebhook` |
| Saber se o seu contrato mudou | `GET /api/v1/contract` (passo 2) |
| Saber quando algo sai do ar | cabeçalhos `Deprecation` e `Sunset` |
| Todas as rotas, campo a campo | `docs/current/api-contract.openapi.yaml`, no repositório do servidor (`sync-zap`) |
