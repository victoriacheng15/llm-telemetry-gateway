package step_definitions

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/cucumber/godog"
	"llm-telemetry-gateway/e2e/support"
)

func RegisterPIISteps(ctx *godog.ScenarioContext, state *support.TestState) {
	ctx.Step(`^the PII policy engine is running$`, func() error {
		return state.SetupUDS()
	})

	ctx.Step(`^the Completions Proxy is running$`, func() error {
		return state.SetupProxy()
	})

	ctx.Step(`^I send a completion request with prompt "([^"]*)"$`, func(prompt string) error {
		reqBody, err := json.Marshal(map[string]interface{}{
			"model": "ollama",
			"messages": []map[string]string{
				{"role": "user", "content": prompt},
			},
		})
		if err != nil {
			return err
		}

		resp, err := http.Post(state.ProxyAddr+"/v1/chat/completions", "application/json", bytes.NewBuffer(reqBody))
		if err != nil {
			return err
		}
		state.LastResponse = resp

		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		state.LastBody = string(bodyBytes)
		return nil
	})
}
