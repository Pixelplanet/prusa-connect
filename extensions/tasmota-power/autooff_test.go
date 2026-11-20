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
