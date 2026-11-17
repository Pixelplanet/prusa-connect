module github.com/prusa3d/tasmota-power

go 1.26.11

require (
	github.com/eclipse/paho.mqtt.golang v1.5.1
	github.com/prusa3d/prusa-connect/sdk v0.0.0-00010101000000-000000000000
)

replace github.com/prusa3d/prusa-connect/sdk => ../../sdk
