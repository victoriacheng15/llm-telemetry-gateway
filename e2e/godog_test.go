package e2e

import (
	"testing"

	"github.com/cucumber/godog"
	"llm-telemetry-gateway/e2e/step_definitions"
	"llm-telemetry-gateway/e2e/support"
)

func TestFeatures(t *testing.T) {
	state := &support.TestState{}

	suite := godog.TestSuite{
		Name: "LLM Telemetry Gateway E2E Suite",
		ScenarioInitializer: func(ctx *godog.ScenarioContext) {
			support.InitializeScenario(ctx, state)
			step_definitions.RegisterCommonSteps(ctx, state)
			step_definitions.RegisterPIISteps(ctx, state)
			step_definitions.RegisterProxySteps(ctx, state)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			TestingT: t,
		},
	}

	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
