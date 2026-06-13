package step_definitions

import (
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
}
