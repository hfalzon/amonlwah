# Amon Lhaw

Load an application's project configuration inside Go. Manage the project's
machine-account token in Amon Hen once; start the Go application normally.
No shell wrapper or token argument is required in everyday use.

> Amon Lhaw, a fortified hill on the Eastern bank of the Anduin. Now in ruins. It was once called the Hill of Hearing.

```go
import lhaw "github.com/hfalzon/amonlwah/go"

if err := lhaw.Load("gadget-dev"); err != nil {
    return err
}
// Existing os.Getenv consumers now see the selected project's values.
```

For tests, call the loader from each participating package's TestMain, using
one shared application-owned test helper if desired:

```go
func TestMain(m *testing.M) {
    if err := lhaw.Load("gadget-dev"); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
    os.Exit(m.Run())
}
```

Run normal Go commands. Packages are separate test processes, so each process
initializes once. Framework libraries need no dependency on Lhaw.

## Project selection and authentication

A selector first resolves as an Amon Hen project name. Internally Lhaw runs
Amon Hen's project lookup and token-wrapped BWS retrieval using argument arrays,
without `bws run` or a command shell. Install Amon Hen and BWS once and make
those executables available on PATH; explicit binary paths are also supported.
The machine-account token stays in the Amon Hen/BWS child process on this path.

If the project does not exist in Amon Hen, the selector is interpreted as an
environment-variable name containing the project UUID. Missing Amon Hen also
allows this direct path. Authentication or provider failures never silently
fall back to local credentials.

An explicit UUID skips Amon Hen lookup:

```go
err := lhaw.LoadWithConfig(lhaw.Config{
    ProjectID: "12345678-1234-4123-8123-123456789abc",
})
```

This direct path requires BWS authentication already configured. A UUID alone
is not authentication. Supplying both Selector and ProjectID is an error.

## Configuration discovery

An optional `.lhaw.json` can contain `selector`, `project_id`, and `dotenv_path`.
Explicit project selection overrides the file's selection. Starting at the
working directory, discovery walks upward to the nearest `.lhaw.json` or
`go.mod`, so nested test packages find module-root configuration. It does not
walk beyond that module boundary. `RootDir` and `ConfigPath` provide explicit
anchors; an explicitly named missing config file is an error. Relative dotenv
paths resolve beside the config file, not beside the test package.

A missing `.env` is normal. Value priority remains provider, dotenv, then the
existing environment. A dotenv BWS token is authentication input only and is
not installed into the application environment. This is compatibility support;
Amon Hen is the recommended local authentication path.

## Reliability and safety

- `LoadContext` and `LoadWithConfigContext` support cancellation. All loads
  have a 30-second default deadline, configurable with `Config.Timeout`.
  Cancellation terminates provider process trees on Windows and Unix.
- Concurrent loads are serialized. The same successfully selected project is
  reused without another provider call. Failures remain retryable. A different
  project requires another process.
- Duplicate provider/dotenv names are rejected, with case-insensitive matching
  on Windows. Reserved runtime and authentication names are rejected before
  installation, protecting PATH, PATHEXT, temporary directories, and BWS control
  variables. Environment validation completes before any value is installed;
  installation failures restore previous values.
- Provider errors exclude captured output, which could contain credentials.
- `Config.Provider` accepts a context-aware `Provider` implementation. Tests can
  use fake providers without installing tools or accessing real credentials.
- Application credentials are installed in the process environment and can be
  read by code in that process and inherited by its children. This is configuration
  loading, not isolation from code running in the application.
- Initialize before starting application goroutines or child processes. Installing
  several environment variables is not an atomic snapshot for unrelated readers.

The default provider currently integrates through the installed CLIs internally.
The provider interface permits a future supported Amon Hen Go API without changing
application startup code. JavaScript and Rust implementations remain planned.

## Verification

Run `go test ./...` from `go/`. Tests use temporary directories, synthetic values,
and helper processes; they do not access the OS credential store or live services.
CI runs these checks on Windows, Linux, and macOS with Go 1.23 and the current Go.
