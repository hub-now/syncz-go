package syncz_test

import (
	"context"
	"regexp"
	"testing"

	syncz "github.com/hub-now/syncz-go"
)

// TestSandboxCriarGrupo_JIDUnicoEntreDubles cobre a tarefa 52 (v0.4.1): dois Dublês (como dois pacotes de teste que
// dividem um banco) criam um grupo cada, e os JIDs diferem. Até a v0.4.0 o JID era só o contador, e o 1º grupo de todo
// Dublê saía igual. O formato continua o do WhatsApp: <dígitos>@g.us.
func TestSandboxCriarGrupo_JIDUnicoEntreDubles(t *testing.T) {
	ctx := context.Background()
	formato := regexp.MustCompile(`^[0-9]+@g\.us$`)

	vistos := map[string]bool{}
	for i := range 2 {
		sbx := syncz.NovoSandbox()
		for j := range 2 {
			g, err := sbx.CriarGrupo(ctx, syncz.EntradaGrupo{InstanciaID: "inst-sbx", Assunto: "Família"})
			if err != nil {
				t.Fatalf("Dublê %d, grupo %d: CriarGrupo falhou: %v", i, j, err)
			}
			if !formato.MatchString(g.JID) {
				t.Errorf("Dublê %d, grupo %d: JID %q fora do formato <dígitos>@g.us", i, j, g.JID)
			}
			if vistos[g.JID] {
				t.Errorf("Dublê %d, grupo %d: JID %q repetido", i, j, g.JID)
			}
			vistos[g.JID] = true
		}
		_ = sbx.Fechar()
	}
}
