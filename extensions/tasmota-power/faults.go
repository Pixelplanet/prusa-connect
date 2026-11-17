package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/prusa3d/prusa-connect/sdk/fault"
)

var faultDomain = mustDomain("com.github.aux.prusa3d.tasmota-power")

var (
	RouteNotFound = fault.Register(faultDomain, "ROUTE_NOT_FOUND", http.StatusNotFound, fault.Document(
		"This module does not serve that path",
		"Check openapi.json for supported paths.",
		fault.RetryNever,
		fault.ShowDetail,
		"path",
	))
	InvalidInput = fault.Register(faultDomain, "INVALID_INPUT", http.StatusBadRequest, fault.Document(
		"Invalid request body",
		"Check the JSON payload and try again.",
		fault.RetryNever,
		fault.ShowDetail,
	))
	NotConfigured = fault.Register(faultDomain, "NOT_CONFIGURED", http.StatusPreconditionFailed, fault.Document(
		"Tasmota plug or MQTT broker is not configured",
		"Configure mqtt_broker in extension settings and set the printer Tasmota topic.",
		fault.RetryNever,
		fault.ShowDetail,
	))
	PrinterBusy = fault.Register(faultDomain, "PRINTER_BUSY", http.StatusConflict, fault.Document(
		"Cannot turn off plug while printer is active",
		"Wait until the printer is idle or finished, or enable Auto-off when print finishes.",
		fault.RetryAllowed,
		fault.ShowDetail,
		"state",
	))
	BrokerOffline = fault.Register(faultDomain, "BROKER_OFFLINE", http.StatusServiceUnavailable, fault.Document(
		"MQTT broker is not connected",
		"Verify the MQTT broker address, credentials, and network reachability.",
		fault.RetryAllowed,
		fault.ShowDetail,
	))
	InternalError = fault.Register(faultDomain, "INTERNAL_ERROR", http.StatusInternalServerError, fault.Document(
		"Internal module error",
		"Check the server logs for details.",
		fault.RetryAllowed,
		fault.ShowDetail,
	))
)

func mustDomain(name string) fault.Domain {
	domain, err := fault.NewDomain(name)
	if err != nil {
		panic(fmt.Sprintf("tasmota-power: %s is not a fault domain: %v", name, err))
	}
	return domain
}

func refuse(w http.ResponseWriter, r *http.Request, raised *fault.Fault) {
	problem := raised.ProblemJSON(r.URL.Path)
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(problem.Status)
	_ = json.NewEncoder(w).Encode(problem)
}
