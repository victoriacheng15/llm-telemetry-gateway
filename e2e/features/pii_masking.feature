Feature: PII Masking
  As a security-conscious completions proxy
  I want to detect and redact sensitive PII before forwarding payloads
  So that user privacy is preserved

  Scenario: SSN is masked in the prompt
    Given the PII policy engine is running
    And the Completions Proxy is running
    When I send a completion request with prompt "My SSN is 123-45-6789"
    Then the response should contain "My SSN is ***-**-****"
    And the response status code should be 200
    But the response should not contain "123-45-6789"

  Scenario: Credit card number is masked in the prompt
    Given the PII policy engine is running
    And the Completions Proxy is running
    When I send a completion request with prompt "Pay using card 1234-5678-9012-3456"
    Then the response should contain "Pay using card ****-****-****-****"
    And the response status code should be 200

  Scenario: Canadian SIN is masked in the prompt
    Given the PII policy engine is running
    And the Completions Proxy is running
    When I send a completion request with prompt "My SIN is 123-456-789"
    Then the response should contain "My SIN is ***-***-***"
    And the response status code should be 200

