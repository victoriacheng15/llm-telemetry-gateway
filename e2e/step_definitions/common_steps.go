package step_definitions

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"
	"llm-telemetry-gateway/e2e/support"
)

func RegisterCommonSteps(ctx *godog.ScenarioContext, state *support.TestState) {
	ctx.Step(`^the response should contain "([^"]*)"$`, func(expected string) error {
		if !strings.Contains(state.LastBody, expected) {
			return fmt.Errorf("expected response to contain %q, but got %q", expected, state.LastBody)
		}
		return nil
	})

	ctx.Step(`^the response should not contain "([^"]*)"$`, func(unexpected string) error {
		if strings.Contains(state.LastBody, unexpected) {
			return fmt.Errorf("expected response to not contain %q, but it did", unexpected)
		}
		return nil
	})

	ctx.Step(`^the response status code should be (\d+)$`, func(expected int) error {
		if state.LastResponse.StatusCode != expected {
			return fmt.Errorf("expected status code %d, but got %d", expected, state.LastResponse.StatusCode)
		}
		return nil
	})
}
