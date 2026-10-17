package syncz

import (
	"encoding/json"
	"testing"

	"github.com/hub-now/syncz-go/synczv1"
	"google.golang.org/protobuf/types/known/structpb"
)

// TestConsentimentoEvidencia (task 603): a evidencia do aceite chega em
// ConsentimentoInfo.Evidencia pelos dois transportes; ausente fica nil.
func TestConsentimentoEvidencia(t *testing.T) {
	var res instanciaRespDTO
	corpo := `{"id":"i1","consent":{"status":"granted","evidence":{"metodo":"pagina-propria"}}}`
	if err := json.Unmarshal([]byte(corpo), &res); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got := instanciaDoResp(res).Consentimento.Evidencia["metodo"]; got != "pagina-propria" {
		t.Errorf("REST: Evidencia[metodo] = %v", got)
	}

	ev, err := structpb.NewStruct(map[string]any{"metodo": "pagina-propria"})
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	if got := consentimentoDoProto(&synczv1.Consent{Status: "granted", Evidence: ev}).Evidencia["metodo"]; got != "pagina-propria" {
		t.Errorf("gRPC: Evidencia[metodo] = %v", got)
	}
	if got := consentimentoDoProto(&synczv1.Consent{Status: "granted"}).Evidencia; got != nil {
		t.Errorf("gRPC sem evidencia: esperava nil, obteve %v", got)
	}
}
