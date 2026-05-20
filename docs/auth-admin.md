# Auth and Admin

## OIDC

The API validates bearer tokens with OIDC issuer metadata. Only admin APIs require authentication; non-admin GET APIs are public.

Required env vars:

- `OIDC_ISSUER_URL`
- `OIDC_AUDIENCE`
- `OIDC_CLIENT_ID`
- `OIDC_REDIRECT_URL`
- `OIDC_SCOPES`

Optional env vars:

- `OIDC_AUTH_URL`
- `OIDC_TOKEN_URL`
- `OIDC_SKIP_ISSUER_CHECK`
- `OIDC_SKIP_AUDIENCE_CHECK`
- `OIDC_PRIVATE_KEY_PATH`
- `OIDC_PRIVATE_KEY_ID`
- `OIDC_ADMIN_CLAIM`
- `OIDC_ADMIN_CLAIM_VALUES`

`OIDC_SCOPES` should include OpenID scopes required by the provider and any API audience/resource scopes required by the deployment.

## Admin RBAC

If `OIDC_ADMIN_CLAIM` and `OIDC_ADMIN_CLAIM_VALUES` are both set, `/api/v1/admin/*` requires a matching claim value.

Claim lookup supports top-level or dotted paths such as:

- `groups`
- `roles`
- `realm_access.roles`

Claim values can be arrays or strings. `scope` and `scp` string claims are split on whitespace and commas.

If RBAC env vars are empty, admin authorization falls back to authenticated-user-only behavior.

## Admin Dashboard

- `GET /admin/login`
- `GET /admin`
- `mise run admin-open`

Login flow:

1. Open `/admin/login`.
2. The dashboard redirects to the configured OIDC authorization endpoint.
3. The backend receives the callback at `/api/v1/admin/login/callback`.
4. The backend exchanges the authorization code with PKCE.
5. If `OIDC_PRIVATE_KEY_PATH` is configured, token exchange uses `private_key_jwt`.
6. The callback page stores the access token in session storage and redirects to `/admin`.

The dashboard can view sync status and trigger normal or force sync for one region or all regions.

## Local Keycloak

`.env.development` is preconfigured for the bundled Keycloak instance.

- Browser Keycloak URL on OrbStack: `http://keycloak.sekai-master-api.orb.local`
- Host-mode issuer: `http://localhost:18081/realms/sekai`
- Container-mode issuer for `mise run dev`: `http://keycloak:8080/realms/sekai`
- Client ID / audience: `sekai-api`
- Redirect URI: `http://localhost:18080/api/v1/admin/login/callback`
- Admin RBAC claim: `groups`
- Required admin value: `sekai-admin`

Bundled local users:

- Test login user: `alice`
- Test login password: `alice123!`
- Keycloak bootstrap admin: `admin`
- Keycloak bootstrap admin password: `admin`

Fetch a local access token without browser login:

```sh
mise run keycloak-token
```

Override local values in `.env.development.local` when needed.
