# Contributing

## Setup

```sh
cp .env.example .env
mise trust
mise install
mise run tidy
```

## Validation

Before opening a pull request, run:

```sh
mise run lint
mise run test
```

For API or OpenAPI changes, also run:

```sh
mise run swagger
```

## Pull requests

- Keep changes focused and easy to review.
- Include tests for behavior changes, documentation for user-facing or workflow changes, and updates to `.env.example` for configuration changes.
- Include generated Swagger output when the API contract changes.
- Preserve compatibility unless a breaking change is explicitly intended and documented.
- Use Conventional Commits-style commit messages.
- Never commit secrets, tokens, or other sensitive credentials.
