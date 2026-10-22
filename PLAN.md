# Amon LHAW design plan

## Goal

Amon LHAW is a language-neutral library design, implemented in Go first, that
loads a selected project's credentials into the application's runtime
state. The repository reserves separate JavaScript and Rust package locations
for later implementations. Application code reads values through a
language-appropriate `getenv`/`lookupEnv` API, without knowing whether values
came from Bitwarden Secrets Manager, a `.env` file, or the process environment.

The app starts normally. It is not launched or wrapped by Amon Hen or LHAW.
Separate app processes can load separate projects at the same time without
sharing LHAW's in-memory values.

## Current Amon Hen integration

The local Amon Hen checkout already has the capabilities LHAW needs:

- `amonhen project uuid -N <name>` resolves an Amon Hen project name to its
  BWS project UUID.
- `amonhen token -N <name> -- <command>` retrieves that project's token from
  the OS credential store and gives it to the child command as
  `BWS_ACCESS_TOKEN`.
- BWS can list the selected project's secrets as JSON.

For the first LHAW version, use these existing commands. No API that returns
raw tokens to LHAW is required. LHAW starts the BWS command through Amon Hen and
captures the JSON response. Amon Hen does not launch the application.

## Initial Go API

```go
if err := lhaw.Load("local-dev"); err != nil {
    log.Fatal(err)
}

databaseURL := lhaw.Getenv("DATABASE_URL")
apiKey, ok := lhaw.LookupEnv("API_KEY")
```

`Load` should accept an optional selector. An explicit selector takes
precedence; when omitted, LHAW reads an optional project selector from its
configuration. The selector is not a credential.

`Load` resolves sources and installs the resolved values into the current
process environment. `Getenv` and `LookupEnv` delegate to Go's standard
environment lookup behavior. Existing application code using `os.Getenv`
therefore sees LHAW-loaded values without needing to migrate every call site.
`Load` returns errors for invalid configuration or a configured provider that
fails to authenticate or load.

## Project and credential resolution

When a selector is supplied:

1. Treat it as an Amon Hen project name first.
2. If Amon Hen has that project, resolve its UUID with
   `amonhen project uuid -N <name>`, then retrieve the BWS JSON through
   `amonhen token -N <name> -- bws secret list <uuid>`.
3. If there is no matching Amon Hen project, treat the selector as the name of
   an environment variable. Its value is the BWS project UUID. Authenticate
   directly to BWS using supported direct configuration, such as
   `BWS_ACCESS_TOKEN` or BWS CLI configuration.

If the selector is absent, LHAW can load `.env` and process-environment values
without a BWS layer. If a selector points to a configured Amon Hen project or a
direct BWS setup is present but authentication/retrieval fails, return an
error. Do not silently substitute another credential source after a provider
failure.

## Value lookup precedence

LHAW loads candidate values into separate maps, resolves conflicts in this
order, and installs the result in the current process environment:

1. Selected BWS project's secrets
2. `.env` values
3. Values from the process environment at load time

If a key is absent from one layer, lookup proceeds to the next. A provider
failure is an error; a missing key is not. Duplicate keys use the first value
in the precedence order above. Existing process variables remain as the final
fallback and are not overwritten unless a higher-precedence source supplies
that key.

Parse `.env` into a private map first so source precedence remains explicit.
After resolution, use `os.Setenv` to install values in this application
process, allowing existing `os.Getenv` consumers and dotenv loaders that
preserve already-set variables to work. LHAW does not write retrieved values
to files or change machine-wide settings.

## Runtime and concurrency model

- One LHAW load corresponds to one project per Go process.
- Load values into the process environment; do not persist a shared cache.
- Separate app processes may load different projects concurrently. Each calls
  `lhaw.Load` with its own selector and retains its own values.
- Make initialization and lookups safe against concurrent goroutines in the
  same process. Reject or clearly define repeated loads for different projects
  in one process; multi-project use inside one process is outside the first
  version's scope.
- Clear temporary byte buffers where practical after parsing. Go strings and
  maps cannot provide reliable secret zeroization, so values remain in memory
  for the process lifetime.

## Implementation phases

### Phase 1: Source and configuration model

- Define the optional selector/config behavior and supported direct BWS
  authentication sources.
- Define exact handling for missing Amon Hen, unknown Amon Hen project,
  missing selector environment variable, absent BWS credentials, and provider
  errors.
- Keep the value-source priority and provider-authentication selection as
  separate concepts.

### Phase 2: Amon Hen and BWS provider

- Implement subprocess invocation with argument arrays, never a shell-built
  command string.
- Resolve project UUID by the Amon Hen project name.
- Capture BWS JSON from the Amon Hen token-wrapped BWS child process.
- Parse secret keys and values into LHAW's BWS layer.
- Capture stderr for diagnostics, but never log token-bearing environment or
  secret values.
- Implement direct BWS authentication for the no-Amon-Hen path.

### Phase 3: Local value sources and lookup API

- Parse an optional `.env` file into its own layer.
- Snapshot the process environment into a separate layer.
- Implement `Load`, `Getenv`, and `LookupEnv` with the documented precedence.
- Make `Load` fail clearly on configured-source errors and distinguish those
  from a missing individual variable.

### Phase 4: Documentation and verification

- Document local setup with `amonhen project add` and an app calling
  `lhaw.Load("local-dev")` directly.
- Document direct BWS, `.env`, and process-environment fallback behavior.
- Verify precedence, missing values, provider errors, and two simultaneous
  processes loading different Amon Hen projects.

### Later language implementations

- Keep source resolution and security behavior language-neutral.
- Implement JavaScript and Rust packages in their reserved directories after
  stabilizing the Go API and provider contract. Go lives in its own `go/`
  package directory alongside `javascript/` and `rust/`.
- Preserve the process-local environment behavior required for compatibility
  with each language's standard environment lookup APIs.

## Security boundary

Amon Hen keeps its stored token in the OS credential store and passes it to the
BWS child process. LHAW loads the resulting application secret values into the
app process environment. The values are visible to code that can inspect or
control that process and may be inherited by child processes. This avoids
passing the BWS token into application configuration, but does not isolate
secrets from an agent that can invoke Amon Hen or inspect or control the
app/BWS runtime as the same OS user.

Use narrowly scoped, preferably read-only BWS machine accounts. Do not write
tokens or secret values to source files, `.env` files, command arguments,
terminal output, logs, crash reports, or shared run artifacts.
