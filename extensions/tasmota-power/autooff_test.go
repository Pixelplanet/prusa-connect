package main

import (
	"testing"
	"time"
)

// The moments of a printer the auto-off logic tells apart.
var (
	printerPrinting = printerSafety{Connected: true, PrinterState: "PRINTING", ActiveJob: true}
	printerFinished = printerSafety{Connected: true, PrinterState: "FINISHED", CanTurnOff: true}
	printerIdle     = printerSafety{Connected: true, PrinterState: "IDLE", CanTurnOff: true}
	printerHeating  = printerSafety{Connected: true, PrinterState: "BUSY"}
	printerAway     = printerSafety{PrinterState: "PRINTING", CanTurnOff: true}
)

func TestAutoOffWaitsForThePrintAndTheCooldown(t *testing.T) {
	m := newModule(nil)
	start := time.Now()
	wait := 2 * time.Minute

	if m.autoOffDue("p", printerIdle, wait, start) {
		t.Fatal("an idle printer that printed nothing was due")
	}
	if m.autoOffDue("p", printerPrinting, wait, start) {
		t.Fatal("a printing printer was due")
	}
	if m.autoOffDue("p", printerAway, wait, start.Add(time.Hour)) {
		t.Fatal("a printer that lost its connection mid-print was due")
	}
	if m.autoOffDue("p", printerFinished, wait, start.Add(time.Hour)) {
		t.Fatal("the plug was due before the wait began")
	}
	if m.autoOffDue("p", printerFinished, wait, start.Add(time.Hour+time.Minute)) {
		t.Fatal("the plug was due before the wait was over")
	}
	if !m.autoOffDue("p", printerFinished, wait, start.Add(time.Hour+wait)) {
		t.Fatal("the plug was not due after the print and the wait")
	}
}

func TestAPrinterThatTurnsOffByDefaultStaysArmed(t *testing.T) {
	m := newModule(nil)
	now := time.Now()
	cfg := DeviceConfig{Driver: DriverTasmota, TasmotaTopic: "plug", AutoOffAfterPrint: true, AutoOffDefault: true}

	m.autoOffDue("p", printerPrinting, 0, now)
	if !m.autoOffDue("p", printerFinished, 0, now) {
		t.Fatal("the plug was not due after the first print")
	}
	cfg = m.afterAutoOff("p", cfg)
	if !cfg.AutoOffAfterPrint {
		t.Fatal("a printer that turns off by default was disarmed after one print")
	}

	// Turned on again by hand: it still reports the old print as finished,
	// and it is heated and moved. None of that is a print.
	for _, moment := range []printerSafety{printerFinished, printerIdle, printerHeating, printerIdle} {
		if m.autoOffDue("p", moment, 0, now.Add(time.Hour)) {
			t.Fatalf("the plug was due again with no new print, in state %s", moment.PrinterState)
		}
	}

	m.autoOffDue("p", printerPrinting, 0, now.Add(2*time.Hour))
	if !m.autoOffDue("p", printerFinished, 0, now.Add(3*time.Hour)) {
		t.Fatal("the plug was not due after the second print")
	}
}

func TestAOneTimeChoiceGoesBackToTheDefault(t *testing.T) {
	m := newModule(nil)

	once := m.afterAutoOff("p", DeviceConfig{AutoOffAfterPrint: true})
	if once.AutoOffAfterPrint {
		t.Fatal("a choice for one print stayed on for the next")
	}

	// Switched off for one print on a printer that turns off by default.
	steps := []struct {
		moment printerSafety
		over   bool
	}{
		{printerIdle, false},
		{printerPrinting, false},
		{printerFinished, false},
		{printerHeating, false},
		{printerAway, false},
		{printerIdle, false},
		{printerPrinting, true},
	}
	for i, step := range steps {
		if got := m.skippedPrintIsOver("p", step.moment); got != step.over {
			t.Fatalf("step %d (%s): over = %v, want %v", i, step.moment.PrinterState, got, step.over)
		}
	}
}

// Saving a printer's plug must not lose what it does after every print.
func TestSavingAPlugKeepsTheAutoOffDefault(t *testing.T) {
	m := newModule(nil)
	cfg := DeviceConfig{Driver: DriverTasmota, TasmotaTopic: "plug", AutoOffAfterPrint: true, AutoOffDefault: true}
	if err := m.saveConfig(t.Context(), "p", cfg); err != nil {
		t.Fatal(err)
	}

	got, err := m.getConfig(t.Context(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if !got.AutoOffDefault || !got.AutoOffAfterPrint {
		t.Fatalf("the default was lost on save: %+v", got)
	}
}

// A plug cut by hand ends the print that was followed. The printer turned on
// again later must not be turned off for a print that is long over.
func TestAPlugTurnedOffByHandEndsThePrintThatWasFollowed(t *testing.T) {
	now := time.Now()
	wait := 2 * time.Minute

	for name, before := range map[string][]printerSafety{
		"during the wait": {printerPrinting, printerFinished},
		"mid-print":       {printerPrinting},
	} {
		m := newModule(nil)
		for _, moment := range before {
			m.autoOffDue("p", moment, wait, now)
		}
		m.forgetPrint("p")

		for _, later := range []time.Duration{24 * time.Hour, 24*time.Hour + wait} {
			if m.autoOffDue("p", printerIdle, wait, now.Add(later)) {
				t.Fatalf("%s: the printer was turned off again %s later with no new print", name, later)
			}
		}
	}

	m := newModule(nil)
	cfg := DeviceConfig{Driver: DriverTasmota, TasmotaTopic: "plug_a"}
	if m.plugIsOff(cfg) {
		t.Fatal("a plug nothing was heard from counted as off")
	}
	plug := &PlugTelemetry{Topic: "plug_a", Power: "OFF"}
	plug.setRelayState(1, "OFF")
	m.plugs[cfg.plugKey()] = plug
	if !m.plugIsOff(cfg) {
		t.Fatal("a plug that reported OFF did not count as off")
	}
}

// A print seen to be over stays over when the printer then goes away.
func TestTheWaitRunsOnWhenThePrinterGoesAwayAfterThePrint(t *testing.T) {
	m := newModule(nil)
	now := time.Now()
	wait := 2 * time.Minute
	away := printerSafety{CanTurnOff: true}

	m.autoOffDue("p", printerPrinting, wait, now)
	m.autoOffDue("p", printerFinished, wait, now)
	if m.autoOffDue("p", away, wait, now.Add(time.Minute)) {
		t.Fatal("the plug was due before the wait was over")
	}
	if !m.autoOffDue("p", away, wait, now.Add(wait)) {
		t.Fatal("a printer that went away after its print stayed on")
	}
}

// A printer turns off only after a print that finished. A cancelled or a
// failed print leaves it on and leaves the choice armed for the next print.
func TestOnlyAFinishedPrintTurnsThePrinterOff(t *testing.T) {
	now := time.Now()
	wait := 2 * time.Minute
	long := now.Add(24 * time.Hour)

	for name, tc := range map[string]struct {
		end  printerSafety
		kept string
	}{
		"cancelled, by the printer's state": {printerSafety{Connected: true, PrinterState: "STOPPED", CanTurnOff: true}, "cancelled"},
		"failed, by the printer's state":    {printerSafety{Connected: true, PrinterState: "ERROR", CanTurnOff: true}, "failed"},
		"cancelled, by the server's record": {printerSafety{Connected: true, PrinterState: "IDLE", CanTurnOff: true, LastJobID: "job-1", LastJobOutcome: "cancelled"}, "cancelled"},
		"failed, by the server's record":    {printerSafety{Connected: true, PrinterState: "IDLE", CanTurnOff: true, LastJobID: "job-1", LastJobOutcome: "failed"}, "failed"},
	} {
		m := newModule(nil)
		running := printerPrinting
		running.JobID = "job-1"
		m.autoOffDue("p", running, wait, now)
		if m.autoOffDue("p", tc.end, wait, now) || m.autoOffDue("p", tc.end, wait, long) {
			t.Fatalf("%s: the printer was turned off", name)
		}
		if got := m.trackers["p"].kept; got != tc.kept {
			t.Fatalf("%s: kept = %q, want %q", name, got, tc.kept)
		}

		// The retry is a new print, and it finishes.
		m.autoOffDue("p", running, wait, long)
		if m.trackers["p"].kept != "" {
			t.Fatalf("%s: the reason outlived the next print", name)
		}
		m.autoOffDue("p", printerFinished, wait, long)
		if !m.autoOffDue("p", printerFinished, wait, long.Add(wait)) {
			t.Fatalf("%s: the retry that finished did not turn the printer off", name)
		}
	}
}

func TestAPausedPrintNeverTurnsThePrinterOff(t *testing.T) {
	m := newModule(nil)
	now := time.Now()
	paused := printerSafety{Connected: true, PrinterState: "PAUSED", ActiveJob: true}

	m.autoOffDue("p", printerPrinting, 0, now)
	for _, later := range []time.Duration{time.Minute, time.Hour, 72 * time.Hour} {
		if m.autoOffDue("p", paused, 0, now.Add(later)) {
			t.Fatalf("a print paused for %s turned the printer off", later)
		}
	}
}

// The record of another print says nothing about this one, and a print with
// no outcome yet is given a moment before it counts as finished.
func TestAPrintWithNoOutcomeYetIsGivenAMoment(t *testing.T) {
	m := newModule(nil)
	now := time.Now()
	running := printerPrinting
	running.JobID = "job-2"
	stale := printerSafety{Connected: true, PrinterState: "IDLE", CanTurnOff: true, LastJobID: "job-1", LastJobOutcome: "finished"}

	m.autoOffDue("p", running, 0, now)
	if m.autoOffDue("p", stale, 0, now) {
		t.Fatal("the printer was turned off on the record of an older print")
	}
	recorded := stale
	recorded.LastJobID, recorded.LastJobOutcome = "job-2", "cancelled"
	if m.autoOffDue("p", recorded, 0, now.Add(5*time.Second)) || m.autoOffDue("p", recorded, 0, now.Add(time.Hour)) {
		t.Fatal("a print recorded as cancelled a moment later turned the printer off")
	}

	n := newModule(nil)
	n.autoOffDue("p", running, 0, now)
	n.autoOffDue("p", stale, 0, now)
	if !n.autoOffDue("p", stale, 0, now.Add(outcomeGrace)) {
		t.Fatal("a print nothing was recorded about never turned the printer off")
	}
}

// A print skipped on a printer that turns off by default, and then cancelled,
// is not used up: the print started in its place is skipped too.
func TestASkippedPrintThatWasCancelledIsSkippedAgain(t *testing.T) {
	m := newModule(nil)
	stopped := printerSafety{Connected: true, PrinterState: "STOPPED", CanTurnOff: true}

	for i, moment := range []printerSafety{printerPrinting, stopped, printerPrinting, printerFinished} {
		if m.skippedPrintIsOver("p", moment) {
			t.Fatalf("step %d (%s): the default came back before a print finished", i, moment.PrinterState)
		}
	}
	if !m.skippedPrintIsOver("p", printerPrinting) {
		t.Fatal("the default did not come back for the print after the one that finished")
	}
}

func TestAPlugThatWillNotTurnOffIsReported(t *testing.T) {
	m := newModule(nil)
	now := time.Now()
	m.autoOffDue("p", printerPrinting, 0, now)
	m.autoOffDue("p", printerFinished, 0, now)
	m.noteAutoOffFault("p", errBrokerOffline)

	if got := m.trackers["p"].offFault; got != errBrokerOffline.Error() {
		t.Fatalf("the fault was not kept: %q", got)
	}
	if !m.autoOffDue("p", printerFinished, 0, now.Add(time.Minute)) {
		t.Fatal("the turn-off was not tried again")
	}
}
