package step_definitions

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/cucumber/godog"
	"llm-telemetry-gateway/e2e/support"
)

func RegisterProxySteps(ctx *godog.ScenarioContext, state *support.TestState) {
	ctx.Step(`^the PII policy engine is stopped$`, func() error {
		state.StopUDS()
		return nil
	})

	ctx.Step(`^I GET path "([^"]*)"$`, func(path string) error {
		resp, err := http.Get(state.ProxyAddr + path)
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

	ctx.Step(`^I send an OPTIONS request to path "([^"]*)"$`, func(path string) error {
		req, err := http.NewRequest(http.MethodOptions, state.ProxyAddr+path, nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
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

	ctx.Step(`^I send a masking request with prompt "([^"]*)"$`, func(prompt string) error {
		reqBody, err := json.Marshal(map[string]string{"prompt": prompt})
		if err != nil {
			return err
		}
		resp, err := http.Post(state.ProxyAddr+"/api/mask", "application/json", bytes.NewBuffer(reqBody))
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
