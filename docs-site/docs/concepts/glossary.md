---
title: Glossary
sidebar_position: 99
---

# Glossary

## OIDC groups claim

An OpenID Connect claim, usually named `groups`, that contains the identity-provider groups assigned to the authenticated user. Cordum reads this claim for Okta-style RBAC onboarding when `CORDUM_OIDC_GROUPS_CLAIM` is configured.

## GroupRoleMapping

A Cordum OIDC configuration map from identity-provider group names to Cordum roles. Group keys are matched case-insensitively after trimming whitespace, values must be `admin`, `operator`, or `viewer`, and duplicate normalized group keys are rejected.