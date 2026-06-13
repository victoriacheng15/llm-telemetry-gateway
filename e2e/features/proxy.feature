Feature: Completions Proxy Routing and Error Paths
  As an API consumer
  I want to verify the HTTP endpoints and error handling of the completions proxy
  So that I can ensure the proxy is robust and reliable

  Scenario: Health check endpoint is healthy
    Given the Completions Proxy is running
    When I GET path "/healthz"
    Then the response status code should be 200
    And the response should contain "healthy"

  Scenario: Ready check endpoint is unready when PII engine is down
    Given the PII policy engine is stopped
    And the Completions Proxy is running
    When I GET path "/readyz"
    Then the response status code should be 503
    And the response should contain "unready"

  Scenario: Ready check endpoint is ready when PII engine is running
    Given the PII policy engine is running
    And the Completions Proxy is running
    When I GET path "/readyz"
    Then the response status code should be 200
    And the response should contain "ready"

  Scenario: Completions endpoint rejects GET request
    Given the Completions Proxy is running
    When I GET path "/v1/chat/completions"
    Then the response status code should be 405

  Scenario: Completions endpoint returns service unavailable when PII engine is down
    Given the PII policy engine is stopped
    And the Completions Proxy is running
    When I send a completion request with prompt "hello"
    Then the response status code should be 503
    And the response should contain "PII policy engine unreachable"
