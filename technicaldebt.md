# Technical Debt & Findings

## v1 Contract Failure (NON_APPLYING_DIFF)
- **Model**: qwen2.5:14b
- **Issue**: Hallucinated line counts in unified diff (hunks didn't match actual file)
- **Root Cause**: Asking models to do arithmetic on line numbers is incidental complexity
- **Resolution**: Switched to contract v2 (complete file contents, git computes diff)
- **Published**: Yes, in results.jsonl

## v2 Contract Intent Violation (INTENT_VIOLATION)
- **Model**: qwen2.5:7b
- **Issue**: Replaced entire file instead of augmenting it
- **Automated Gates**: All passed (diff applied, build OK, tests OK)
- **Human Review**: Caught deletion of Sum/Mean/Window functions
- **Lesson**: Automated verification is blind to missing functionality unless negative test coverage exists
- **Action Item**: Add negative test cases to eval tasks (verify old functions still callable)
- **Published**: Yes, in results.jsonl
