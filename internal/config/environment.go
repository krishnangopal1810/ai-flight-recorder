package config

import "os"

func ClientEnvironment() (string, string) {
	url := os.Getenv("FLIGHT_URL")
	if url == "" {
		url = "http://127.0.0.1:7777"
	}
	return url, os.Getenv("FLIGHT_TOKEN")
}

func OCIEnvironment() string {
	value := os.Getenv("FLIGHT_OCI_RUNTIME")
	if value == "podman" {
		return value
	}
	return "docker"
}

type GatewayEnvironment struct{ AllowedHosts, OutputPath string }

func LoadGatewayEnvironment() GatewayEnvironment {
	output := os.Getenv("FLIGHT_GATEWAY_OUTPUT")
	if output == "" {
		output = "/output"
	}
	return GatewayEnvironment{AllowedHosts: os.Getenv("FLIGHT_GATEWAY_ALLOWED_HOSTS"), OutputPath: output}
}
