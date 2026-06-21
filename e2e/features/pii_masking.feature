Feature: PII Masking
  As a security-conscious completions proxy
  I want to detect and redact sensitive PII before forwarding payloads
  So that user privacy is preserved

  Background:
    Given the PII policy engine is running
    And the Completions Proxy is running

  Scenario Outline: Sensitive PII masking in prompts
    When I send a completion request with prompt "<prompt>"
    Then the response should contain "<expected>"
    And the response status code should be 200
    And the response should not contain "<raw_pii>"

    Examples:
      | prompt                              | expected                            | raw_pii             |
      | My SSN is 123-45-6789               | My SSN is ***-**-****               | 123-45-6789         |
      | Pay using card 1234-5678-9012-3456  | Pay using card ****-****-****-****  | 1234-5678-9012-3456 |
      | My SIN is 123-456-789               | My SIN is ***-***-***               | 123-456-789         |

