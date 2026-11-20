package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

// Asking the host whether the person behind a request may do what it asks.
//
// The host takes view_resource on this module before it passes a request on,
// so every route is already closed to a person who may not see the module.
// The routes that change something ask the host once more:
//
//   - switching a plug and the auto-off of one print is operate_resource on
//     the module's resource kind, and operate on that printer;
//   - the connection settings, the broker and Home Assistant tests, plug
//     discovery and the plug list are configure_resource on the module's
//     resource kind;
//   - a printer's plug is configure_resource on the module's resource kind,
//     and operate on that printer.
//
// Each request asks the host again. The host answers under the credential of
// the request that is open (a token's scope narrows what the person holds),
// so an answer kept for one request could be wrong for the next.
//
// The actions are declared under checks: in module.yaml.

// accessChecker is the host's access.check, which a test replaces.
type accessChecker interface {
	Check(ctx context.Context, params accessapi.CheckParams) (accessapi.Decision, error)
}

// The resource kind this module is an instance of, as module.yaml declares
// it with kind:.
const resourceKind = "sink"

// The actions this module asks about. Each is listed under checks: in
// module.yaml.
const (
	actionOperate           = "operate"
	actionOperateResource   = "operate_resource"
	actionConfigureResource = "configure_resource"
)

// right is what a route asks of the person.
type right int

const (
	// mayOperate is switching a printer's plug: operate_resource on the
	// module's kind and operate on the printer named in the path.
	mayOperate right = iota + 1
	// mayConfigure is changing the module's set-up: configure_resource on the
	// module's kind.
	mayConfigure
	// mayConfigureDevice is setting a printer's plug: configure_resource on
	// the module's kind and operate on the printer named in the path.
	mayConfigureDevice
)

// question is one access.check the module asks.
type question struct{ person, action, objectType, objectID string }

// ask puts one question to the host.
func ask(ctx context.Context, access accessChecker, q question) (bool, error) {
	decision, err := access.Check(ctx, accessapi.CheckParams{
		Subject: q.person, Action: q.action, ObjectType: q.objectType, ObjectID: q.objectID,
	})
	if err != nil {
		return false, fmt.Errorf("ask whether %s may %s %s %s: %w", q.person, q.action, q.objectType, q.objectID, err)
	}
	return decision.Allowed, nil
}

// principalOf is the person a request was made for, or "" when the request
// did not come through the host.
func (m *module) principalOf(r *http.Request) string {
	if m.principal != nil {
		return m.principal(r)
	}
	return routesapi.PrincipalOf(r)
}

// questionsFor is what a person must be allowed to hold need on the printer
// deviceID (empty for a route that names none).
func questionsFor(person string, need right, deviceID string) []question {
	onPrinter := func(qs []question) []question {
		if deviceID != "" {
			qs = append(qs, question{person, actionOperate, "device", deviceID})
		}
		return qs
	}
	switch need {
	case mayOperate:
		return onPrinter([]question{{person, actionOperateResource, "resource_kind", resourceKind}})
	case mayConfigureDevice:
		return onPrinter([]question{{person, actionConfigureResource, "resource_kind", resourceKind}})
	default:
		// Anything that is not operate is configure, so a right added later
		// and not handled here is the stricter one.
		return []question{{person, actionConfigureResource, "resource_kind", resourceKind}}
	}
}

// allowed asks the host whether the person behind r holds need. It answers
// the refusal itself and returns false when the request may not go on.
func (m *module) allowed(w http.ResponseWriter, r *http.Request, need right) bool {
	person := m.principalOf(r)
	if person == "" {
		// An empty subject is the module itself in access.check, so a request
		// without a person is refused here and never asked about.
		refuse(w, r, NoPrincipal.New("the request carries no signed-in person"))
		return false
	}
	if m.access == nil {
		refuse(w, r, AccessUnknown.New("the module has no connection to the host's access checks"))
		return false
	}

	for _, q := range questionsFor(person, need, r.PathValue("device_id")) {
		ok, err := ask(r.Context(), m.access, q)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %s %s: %v\n", logName, r.Method, r.URL.Path, err)
			if errors.Is(err, accessapi.ErrDenied) || errors.Is(err, accessapi.ErrInvalidQuestion) {
				// Asking again gives the same answer: the operator did not
				// approve the checks this module declares.
				refuse(w, r, ChecksNotApproved.New("this server does not let the extension ask whether you may do this. An administrator approves the extension's checks on its Permissions tab."))
				return false
			}
			refuse(w, r, AccessUnknown.New("the server could not say whether you may do this. Try again in a moment."))
			return false
		}
		if !ok {
			refuse(w, r, notAllowed(q))
			return false
		}
	}
	return true
}

// guard runs next only for a person who holds need.
func (m *module) guard(need right, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if m.allowed(w, r, need) {
			next(w, r)
		}
	}
}

// What a page offers a person, a button to switch a plug or a form to change
// the set-up, follows the same questions. Those answers only hide what the
// person could not use, since every route asks again, so they are kept for a
// while: a list of printers polled every few seconds asks the host about each
// printer at most once in hintKeep.

// hintKeep is how long an answer that only decides what a page offers is kept.
// hintKeepFailed is how long a question the host could not answer is kept as
// a no, so a host that cannot answer is not asked about every printer on
// every poll.
const (
	hintKeep       = 30 * time.Second
	hintKeepFailed = 10 * time.Second
)

type hintKey struct {
	person   string
	need     right
	deviceID string
}

type hintAnswer struct {
	allowed bool
	until   time.Time
}

// hintCache keeps the answers mayHint gave, for hintKeep.
type hintCache struct {
	mu   sync.Mutex
	kept map[hintKey]hintAnswer
}

// mayHint answers whether person holds need on deviceID, for what a page
// offers. A question the host cannot answer is a no, kept for hintKeepFailed.
func (m *module) mayHint(ctx context.Context, person string, need right, deviceID string) bool {
	if person == "" || m.access == nil {
		return false
	}
	key, now := hintKey{person, need, deviceID}, time.Now()

	m.hints.mu.Lock()
	k, ok := m.hints.kept[key]
	m.hints.mu.Unlock()
	if ok && now.Before(k.until) {
		return k.allowed
	}

	allowed, keep := true, hintKeep
	for _, q := range questionsFor(person, need, deviceID) {
		yes, err := ask(ctx, m.access, q)
		if err != nil {
			if ctx.Err() != nil {
				// The caller left; the host said nothing about the person.
				return false
			}
			allowed, keep = false, hintKeepFailed
			break
		}
		if !yes {
			allowed = false
			break
		}
	}

	m.hints.mu.Lock()
	defer m.hints.mu.Unlock()
	if m.hints.kept == nil {
		m.hints.kept = make(map[hintKey]hintAnswer)
	}
	for other, answer := range m.hints.kept {
		if !now.Before(answer.until) {
			delete(m.hints.kept, other)
		}
	}
	m.hints.kept[key] = hintAnswer{allowed: allowed, until: now.Add(keep)}
	return allowed
}
