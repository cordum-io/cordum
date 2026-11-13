# Changelog

## [Unreleased]

- Published type declarations are now self-contained. The openapi-fetch types are inlined, so TypeScript consumers no longer hit TS2307 and no longer get an any-typed generated client under skipLibCheck.
- CI now runs the sdk-typescript workflow on OpenAPI spec changes, fails on generated-schema drift, and smoke-tests the packed tarball in a clean consumer.

## 0.1.0

- Initial TypeScript SDK bootstrap.
