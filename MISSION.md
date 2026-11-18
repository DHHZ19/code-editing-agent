# Mission: OAuth and OIDC

## Why
Build a durable conceptual model of how “Sign in with Google” works, so the user can reason about identity, authorization, tokens, and trust boundaries rather than memorizing a sequence of API calls.

## Success looks like
- Trace a Google sign-in from the browser redirect through code exchange to the app session.
- Explain the distinct jobs of OAuth 2.0 and OpenID Connect, and what each token is for.
- Identify what an app must validate before trusting an identity response.

## Constraints
- Start from the question “What is OAuth doing under the hood when you ‘Sign in with Google’?”
- Favor standards and provider documentation; teach one compact concept at a time.

## Out of scope
- Implementing a particular framework’s Google sign-in integration.
- A full survey of every OAuth grant or enterprise identity protocol.
