---
title: Okta OIDC groups to Cordum roles
sidebar_position: 1
---

# Okta OIDC groups to Cordum roles

Cordum can map a standard Okta `groups` claim to Cordum roles. This avoids creating a custom per-user `cordum_role` claim and lets Okta group membership remain the source of truth for dashboard and API RBAC.

## 1. Create the Okta OIDC app

1. In Okta Admin, open **Applications → Applications → Create App Integration**.
2. Select **OIDC - OpenID Connect** and **Web Application**.
3. Set the sign-in redirect URI to your gateway callback:
   `https://<gateway-host>/api/v1/auth/sso/oidc/callback`.
4. Save the app and record:
   - issuer URL, usually `https://<okta-domain>/oauth2/default`
   - client ID
   - client secret
   - redirect URI
5. Assign the Okta groups that should be allowed to use Cordum.

## 2. Add the Okta groups claim

1. Open **Security → API → Authorization Servers**.
2. Select the authorization server used by the app, then open **Claims**.
3. Add a claim:
   - **Name:** `groups`
   - **Include in token type:** ID Token, and Access Token if API bearer tokens will also be used
   - **Value type:** Groups
   - **Filter:** choose a prefix or regex that only emits Cordum-relevant groups, for example `cordum-.*`
4. Test with a user assigned to a group such as `cordum-admins`.

## 3. Configure Cordum

Set the standard OIDC values and the group mapping:

```bash
CORDUM_OIDC_ENABLED=true
CORDUM_OIDC_ISSUER=https://<okta-domain>/oauth2/default
CORDUM_OIDC_AUDIENCE=api://default
CORDUM_OIDC_CLIENT_ID=<okta-client-id>
CORDUM_OIDC_CLIENT_SECRET=<okta-client-secret>
CORDUM_OIDC_REDIRECT_URI=https://<gateway-host>/api/v1/auth/sso/oidc/callback
CORDUM_OIDC_GROUPS_CLAIM=groups
CORDUM_OIDC_GROUP_ROLE_MAPPING='{"cordum-admins":"admin","cordum-operators":"operator","cordum-viewers":"viewer"}'
```

You can also edit the claim name and JSON mapping from **Settings → SSO providers → OIDC RBAC mapping**. The dashboard save path updates the live gateway provider and persists the same values into `system/default` config for restart survival.

## Resolution semantics

- Group names are matched case-insensitively after trimming whitespace.
- Mapping values must be exactly `admin`, `operator`, or `viewer`.
- When the configured groups claim is present and non-empty, groups win over the legacy `cordum_role` claim.
- If the groups claim is present and non-empty but none of the groups match the mapping, Cordum uses `DefaultRole`.
- If the groups claim is present but empty, Cordum falls through to `cordum_role`, then `DefaultRole`.
- If multiple groups match, Cordum chooses the highest privilege: `admin` > `operator` > `viewer`.
- Duplicate configured group keys that collide after case-insensitive normalization are rejected.

## Quick verification

1. Sign in through **Test OIDC login** on the SSO settings page.
2. Confirm the session role matches the strongest mapped Okta group.
3. Remove the user from the admin group in Okta and sign in again; the Cordum role should drop to the next mapped group or `DefaultRole`.
4. Keep `cordum_role` unset for normal Okta users; it is only a legacy fallback when the groups claim is absent or empty.