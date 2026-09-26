package api

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/wyw14/solo-035/internal/service"
)

func newSlateTestRouter(t *testing.T) (http.Handler, *service.Runtime) {
	t.Helper()
	runtime, err := service.NewRuntime(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Bootstrap(runtime); err != nil {
		t.Fatal(err)
	}
	var web fs.FS = fstest.MapFS{"slates.html": &fstest.MapFile{Data: []byte("ok")}}
	return NewServer(runtime, web).Router(), runtime
}

func doSlateRequest(t *testing.T, router http.Handler, method string, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestSlateMaterialLifecycleAPI(t *testing.T) {
	router, _ := newSlateTestRouter(t)

	if recorder := doSlateRequest(t, router, http.MethodGet, "/api/slates/materials", nil); recorder.Code != http.StatusOK {
		t.Fatalf("list materials status = %d", recorder.Code)
	}

	recorder := doSlateRequest(t, router, http.MethodPost, "/api/slates/materials", map[string]any{
		"name": "信号故障通告静态片", "content_uri": "slate://emergency/notice-1080p.png",
		"checksum": "sha256:9f86d081884c7d659a2feaa0c55ad015", "controlled_by": "播控值班-林静",
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("register material status = %d body = %s", recorder.Code, recorder.Body.String())
	}

	bad := doSlateRequest(t, router, http.MethodPost, "/api/slates/materials", map[string]any{"name": "缺校验"})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid material status = %d", bad.Code)
	}
}

func TestSlateCutInRequiresCompleteConfirmation(t *testing.T) {
	router, runtime := newSlateTestRouter(t)
	event := runtime.Operations()[0]

	status := doSlateRequest(t, router, http.MethodGet, "/api/slates/status?event_id="+string(event.EventID), nil)
	if status.Code != http.StatusOK {
		t.Fatalf("slate status = %d", status.Code)
	}
	var view map[string]any
	if err := json.Unmarshal(status.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	chain, _ := view["chain"].(map[string]any)
	if available, _ := chain["available"].(bool); !available {
		t.Fatalf("bootstrapped chain should be available: %v", chain)
	}

	doSlateRequest(t, router, http.MethodPost, "/api/slates/materials", map[string]any{
		"name": "应急静态片", "content_uri": "slate://emergency/fallback.png",
		"checksum": "sha256:2c26b46b68ffc68ff99b453c1d304134", "controlled_by": "值班员-周一",
	})

	// 链路尚可用时禁止提案
	proposal := doSlateRequest(t, router, http.MethodPost, "/api/slates/propose", map[string]any{
		"event_id": string(event.EventID), "reason": "test", "operator": "op",
	})
	if proposal.Code != http.StatusConflict {
		t.Fatalf("propose while available status = %d body = %s", proposal.Code, proposal.Body.String())
	}

	// 缺少确认令牌/操作人时必须被拒绝
	missing := doSlateRequest(t, router, http.MethodPost, "/api/slates/cut-in", map[string]any{
		"event_id": string(event.EventID), "slate_id": "whatever",
	})
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("cut-in without confirmation status = %d", missing.Code)
	}

	badEvent := doSlateRequest(t, router, http.MethodGet, "/api/slates/status?event_id=not-a-uuid", nil)
	if badEvent.Code != http.StatusBadRequest {
		t.Fatalf("invalid event status = %d", badEvent.Code)
	}
}
