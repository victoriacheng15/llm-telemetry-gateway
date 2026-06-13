package step_definitions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/cucumber/godog"
	"llm-telemetry-gateway/e2e/support"
)

func RegisterSteps(ctx *godog.ScenarioContext, state *support.TestState) {
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

	ctx.Step(`^the response should contain "([^"]*)"$`, func(expected string) error {
		if !strings.Contains(state.LastBody, expected) {
			return fmt.Errorf("expected response to contain %q, but got %q", expected, state.LastBody)
		}
		return nil
	})

	ctx.Step(`^the response status code should be (\d+)$`, func(expected int) error {
		if state.LastResponse.StatusCode != expected {
			return fmt.Errorf("expected status code %d, but got %d", expected, state.LastResponse.StatusCode)
		}
		return nil
	})

	ctx.Step(`^the response should not contain "([^"]*)"$`, func(unexpected string) error {
		if strings.Contains(state.LastBody, unexpected) {
			return fmt.Errorf("expected response to not contain %q, but it did", unexpected)
		}
		return nil
	})
}
