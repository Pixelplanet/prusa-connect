package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
)

// accessFake answers access.check from a table of allowed actions and keeps
// every question it was asked.
type accessFake struct {
	mu      sync.Mutex
	allow   map[string]bool // action -> yes
	fail    error
	written []accessapi.CheckParams
}

func (a *accessFake) Check(_ context.Context, p accessapi.CheckParams) (accessapi.Decision, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.written = append(a.written, p)
	if a.fail != nil {
		return accessapi.Decision{}, a.fail
	}
	return accessapi.Decision{Allowed: a.allow[p.Action]}, nil
}

func (a *accessFake) asked() []accessapi.CheckParams {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.written)
}

// guardedModule is a module whose requests are for person and whose access
// checks are answered by access.
func guardedModule(t *testing.T, person string, access *accessFake) *module {
	t.Helper()
	m := newModule(nil)
	m.setDataDir(t.TempDir())
	m.access = access
	m.principal = func(*http.Request) string { return person }
	return m
}

type routeCase struct {
	method, path, body string
	need               right
}

// guardedRoutes is every route that changes something, or that lists every
// plug, with what it takes.
var guardedRoutes = []routeCase{
	{"PUT", "/settings", `{}`, mayConfigure},
	{"POST", "/broker/test", `{}`, mayConfigure},
	{"POST", "/ha/test", `{}`, mayConfigure},
	{"POST", "/discover", ``, mayConfigure},
	{"GET", "/assignments", ``, mayConfigure},
	{"PUT", "/devices/p1/config", `{}`, mayConfigureDevice},
	{"POST", "/devices/p1/power", `{"action":"on"}`, mayOperate},
	{"POST", "/devices/p1/auto-off", `{"enabled":true}`, mayOperate},
}

func serve(m *module, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	m.routes().ServeHTTP(rec, req)
	return rec
}

func problemCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("refusal is not problem JSON: %v: %s", err, rec.Body.String())
	}
	return p.Code
}

func TestAViewerIsRefusedEveryRouteThatChangesSomething(t *testing.T) {
	for _, rc := range guardedRoutes {
		t.Run(rc.method+" "+rc.path, func(t *testing.T) {
			// A viewer holds neither operate nor configure.
			access := &accessFake{allow: map[string]bool{}}
			m := guardedModule(t, "user:viewer", access)

			rec := serve(m, rc.method, rc.path, rc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
			}
			if code := problemCode(t, rec); !strings.HasSuffix(code, "FORBIDDEN") {
				t.Fatalf("code %q, want FORBIDDEN", code)
			}
			asked := access.asked()
			if len(asked) == 0 || asked[0].Subject != "user:viewer" {
				t.Fatalf("the person was not asked about: %+v", asked)
			}
		})
	}
}

// An operator may switch a plug but not change the set-up.
func TestAnOperatorMaySwitchButNotConfigure(t *testing.T) {
	for _, rc := range guardedRoutes {
		t.Run(rc.method+" "+rc.path, func(t *testing.T) {
			access := &accessFake{allow: map[string]bool{actionOperate: true, actionOperateResource: true}}
			m := guardedModule(t, "user:operator", access)

			rec := serve(m, rc.method, rc.path, rc.body)
			if rc.need != mayOperate {
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
				}
				return
			}
			// The guard let the request through: no plug is set up for p1.
			if rec.Code != http.StatusPreconditionFailed {
				t.Fatalf("status %d, want 412 NOT_CONFIGURED: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestSwitchingAPlugAsksAboutThePrinterInThePath(t *testing.T) {
	access := &accessFake{allow: map[string]bool{actionOperateResource: true}}
	m := guardedModule(t, "user:other", access)

	rec := serve(m, "POST", "/devices/p7/power", `{"action":"on"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 without operate on the printer: %s", rec.Code, rec.Body.String())
	}
	want := []accessapi.CheckParams{
		{Subject: "user:other", Action: actionOperateResource, ObjectType: "resource_kind", ObjectID: resourceKind},
		{Subject: "user:other", Action: actionOperate, ObjectType: "device", ObjectID: "p7"},
	}
	if got := access.asked(); !slices.Equal(got, want) {
		t.Fatalf("asked %+v, want %+v", got, want)
	}
}

func TestAManagerMayConfigure(t *testing.T) {
	access := &accessFake{allow: map[string]bool{actionConfigureResource: true}}
	m := guardedModule(t, "user:manager", access)

	rec := serve(m, "GET", "/assignments", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	want := []accessapi.CheckParams{
		{Subject: "user:manager", Action: actionConfigureResource, ObjectType: "resource_kind", ObjectID: resourceKind},
	}
	if got := access.asked(); !slices.Equal(got, want) {
		t.Fatalf("asked %+v, want %+v", got, want)
	}
}

// An empty subject is the module itself in access.check, so a request with no
// person is refused before anything is asked.
func TestARequestWithoutAPersonIsRefused(t *testing.T) {
	for _, rc := range guardedRoutes {
		t.Run(rc.method+" "+rc.path, func(t *testing.T) {
			access := &accessFake{allow: map[string]bool{
				actionOperate: true, actionOperateResource: true, actionConfigureResource: true,
			}}
			m := guardedModule(t, "", access)

			rec := serve(m, rc.method, rc.path, rc.body)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
			}
			if code := problemCode(t, rec); !strings.HasSuffix(code, "NO_PRINCIPAL") {
				t.Fatalf("code %q, want NO_PRINCIPAL", code)
			}
			if asked := access.asked(); len(asked) != 0 {
				t.Fatalf("asked about nobody: %+v", asked)
			}
		})
	}
}

func TestAQuestionTheHostCannotAnswerIsRefused(t *testing.T) {
	for _, rc := range guardedRoutes {
		t.Run(rc.method+" "+rc.path, func(t *testing.T) {
			access := &accessFake{fail: errors.New("host went away")}
			m := guardedModule(t, "user:manager", access)

			rec := serve(m, rc.method, rc.path, rc.body)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status %d, want 503: %s", rec.Code, rec.Body.String())
			}
			if code := problemCode(t, rec); !strings.HasSuffix(code, "ACCESS_UNKNOWN") {
				t.Fatalf("code %q, want ACCESS_UNKNOWN", code)
			}
		})
	}
}

// The routes that only read one printer or the person's own fleet ask
// nothing more of the host than it already took.
func TestReadingRoutesAskNothing(t *testing.T) {
	access := &accessFake{}
	m := guardedModule(t, "user:viewer", access)

	if rec := serve(m, "GET", "/settings", ""); rec.Code != http.StatusOK {
		t.Fatalf("GET /settings: status %d: %s", rec.Code, rec.Body.String())
	}
	if asked := access.asked(); len(asked) != 0 {
		t.Fatalf("GET /settings asked %+v", asked)
	}
}

// The host answers under the credential of the open request, so every
// request is asked about again and no answer is kept.
func TestEveryRequestIsAskedAgain(t *testing.T) {
	access := &accessFake{allow: map[string]bool{actionConfigureResource: true}}
	m := guardedModule(t, "user:manager", access)

	for range 3 {
		if rec := serve(m, "GET", "/assignments", ""); rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
	}
	if n := len(access.asked()); n != 3 {
		t.Fatalf("asked %d times, want 3", n)
	}

	// The same person, now under a narrower credential, is refused at once.
	access.allow = map[string]bool{}
	if rec := serve(m, "GET", "/assignments", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

func TestSettingAPrintersPlugAsksAboutThePrinter(t *testing.T) {
	access := &accessFake{allow: map[string]bool{actionConfigureResource: true}}
	m := guardedModule(t, "user:kind-manager", access)

	rec := serve(m, "PUT", "/devices/p9/config", `{}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 without operate on the printer: %s", rec.Code, rec.Body.String())
	}
	want := []accessapi.CheckParams{
		{Subject: "user:kind-manager", Action: actionConfigureResource, ObjectType: "resource_kind", ObjectID: resourceKind},
		{Subject: "user:kind-manager", Action: actionOperate, ObjectType: "device", ObjectID: "p9"},
	}
	if got := access.asked(); !slices.Equal(got, want) {
		t.Fatalf("asked %+v, want %+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(m.dataDir, "configs.json")); err == nil {
		t.Fatal("the refused plug was saved")
	}
}

// A question the host will never answer is not offered as one to try again.
func TestUnapprovedChecksAreNotRetryable(t *testing.T) {
	access := &accessFake{fail: fmt.Errorf("wrapped: %w", accessapi.ErrDenied)}
	m := guardedModule(t, "user:manager", access)

	rec := serve(m, "GET", "/assignments", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if code := problemCode(t, rec); !strings.HasSuffix(code, "CHECKS_NOT_APPROVED") {
		t.Fatalf("code %q, want CHECKS_NOT_APPROVED", code)
	}
}

// Every write operation openapi.json documents is one of guardedRoutes, so a
// route added later and not guarded fails here.
func TestEveryWriteRouteIsGuarded(t *testing.T) {
	raw, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	guarded := map[string]bool{}
	for _, rc := range guardedRoutes {
		guarded[rc.method+" "+strings.ReplaceAll(rc.path, "/p1/", "/{device_id}/")] = true
	}
	for path, ops := range doc.Paths {
		for method := range ops {
			key := strings.ToUpper(method) + " " + path
			if key != "GET "+path && !guarded[key] {
				t.Errorf("%s changes something and is not in guardedRoutes", key)
			}
		}
	}
	for key := range guarded {
		method, path, _ := strings.Cut(key, " ")
		if _, ok := doc.Paths[path][strings.ToLower(method)]; !ok {
			t.Errorf("%s is guarded but not in openapi.json", key)
		}
	}
}

// module.yaml must declare under checks: every action the module asks
// about, or the host refuses the question.
func TestEveryActionAskedIsDeclared(t *testing.T) {
	raw, err := os.ReadFile("module.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	inside := false
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "checks:") {
			inside = true
			continue
		}
		if inside && line != "" && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") {
			inside = false
		}
		if action, found := strings.CutPrefix(strings.TrimSpace(line), "- "); inside && found {
			declared = append(declared, strings.TrimSpace(action))
		}
	}
	for _, need := range []right{mayOperate, mayConfigure, mayConfigureDevice} {
		for _, q := range questionsFor("user:x", need, "p1") {
			if !slices.Contains(declared, q.action) {
				t.Errorf("module.yaml checks: does not declare %q", q.action)
			}
		}
	}
}

// The overview says what a person may change, so a page hides what they
// cannot use. Those answers are kept, since the routes ask again.
func TestTheOverviewSaysWhatAPersonMayChange(t *testing.T) {
	access := &accessFake{allow: map[string]bool{actionOperate: true, actionOperateResource: true}}
	m := guardedModule(t, "user:operator", access)
	m.mu.Lock()
	m.configs["p1"] = DeviceConfig{Driver: DriverTasmota, TasmotaTopic: "plug_1"}
	m.configs["p2"] = DeviceConfig{}
	m.mu.Unlock()

	read := func() overviewResponse {
		resp := m.buildOverview(overviewOptions{
			visible:    func(string) bool { return true },
			canOperate: func(id string) bool { return m.mayHint(context.Background(), "user:operator", mayOperate, id) },
		})
		resp.CanConfigure = m.mayHint(context.Background(), "user:operator", mayConfigure, "")
		return resp
	}
	resp := read()
	if resp.CanConfigure {
		t.Fatal("an operator was offered the set-up")
	}
	if !resp.Devices[0].CanOperate || resp.Devices[1].CanOperate {
		t.Fatalf("can_operate = %v, %v; want true for the printer with a plug only", resp.Devices[0].CanOperate, resp.Devices[1].CanOperate)
	}
	asked := len(access.asked())
	read()
	if len(access.asked()) != asked {
		t.Fatal("the hints were asked again within hintKeep")
	}

	nobody := m.mayHint(context.Background(), "", mayOperate, "p1")
	down := &accessFake{fail: errors.New("down")}
	failing := guardedModule(t, "user:x", down)
	if nobody || failing.mayHint(context.Background(), "user:x", mayOperate, "p1") {
		t.Fatal("a hint without a person, or without an answer, offered the control")
	}
	// A host that cannot answer is not asked again on the next poll.
	before := len(down.asked())
	failing.mayHint(context.Background(), "user:x", mayOperate, "p1")
	if len(down.asked()) != before {
		t.Fatal("a failed hint was asked again at once")
	}
}
