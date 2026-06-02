import pytest
from policy import mask_text

@pytest.mark.parametrize(
    "input_data, expected, want_err",
    [
        pytest.param(
            "My US SSN is 123-45-6789", 
            "My US SSN is [REDACTED_SSN]", 
            False, 
            id="us_ssn_masking"
        ),
        pytest.param(
            "Canadian SIN with spaces 123 456 789", 
            "Canadian SIN with spaces [REDACTED_SIN]", 
            False, 
            id="canadian_sin_spaces_masking"
        ),
        pytest.param(
            "Canadian SIN with hyphens 123-456-789", 
            "Canadian SIN with hyphens [REDACTED_SIN]", 
            False, 
            id="canadian_sin_hyphens_masking"
        ),
        pytest.param(
            "Credit Card is 1234-5678-1234-5678", 
            "Credit Card is [REDACTED_CC]", 
            False, 
            id="credit_card_hyphens_masking"
        ),
        pytest.param(
            "No PII in this text prompt", 
            "No PII in this text prompt", 
            False, 
            id="no_pii_clean_payload"
        ),
        pytest.param(
            "Mixed SSN 123-45-6789 and CC 1111 2222 3333 4444", 
            "Mixed SSN [REDACTED_SSN] and CC [REDACTED_CC]", 
            False, 
            id="mixed_pii_payload"
        )
    ]
)
def test_mask_text(input_data, expected, want_err):
    if want_err:
        with pytest.raises(Exception):
            mask_text(input_data)
    else:
        assert mask_text(input_data) == expected
