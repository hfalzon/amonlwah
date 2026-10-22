# Amon Lhaw

Amon LHAW is a language-neutral secret-loading design with its first
implementation as an in-process Go library. It lets an application read
configuration through `lhaw.Getenv` and `lhaw.LookupEnv`, with source
precedence of BWS secrets, `.env`, then the process environment. It never
changes `os.Environ` or writes secret values to disk.

## Go quick start

```go
package main

import (
	"log"

	lhaw "github.com/hfalzon/amonlwah/go"
)

func main() {
	if err := lhaw.Load("local-dev"); err != nil {
		log.Fatal(err)
	}
	apiKey := lhaw.Getenv("API_KEY")
	if databaseURL, ok := lhaw.LookupEnv("DATABASE_URL"); ok {
		_ = databaseURL
	}
	_ = apiKey
}
```

The application starts normally; LHAW loads values inside its process. For
local development, `local-dev` first resolves as an Amon Hen project name.
LHAW asks Amon Hen to run BWS for that project's UUID and reads the returned
JSON. The BWS access token remains with the Amon Hen/BWS child process and is
not added to the application environment. If no such Amon Hen project exists,
the selector is interpreted as the name of a value in `.env` or the process
environment containing a BWS project UUID; direct BWS CLI authentication is
then used.

`Load()` can read an optional `.lhaw.json` selector:

```json
{"selector":"local-dev"}
```

An explicit `Load("...")` selector takes precedence. The `.env` file is
parsed privately and does not mutate the process environment. If a selected
provider is configured but retrieval fails, `Load` returns an error. Missing
individual keys fall through by source precedence.

LHAW supports one selected project per process. Multiple separate app
processes can load different projects concurrently without sharing LHAW's
in-memory state. Any code that can inspect or control the app process can
access values that the app has loaded; use appropriately scoped BWS accounts.

## Repository layout

- `go/`: Go module and initial library implementation.
- `javascript/`: reserved for the future JavaScript package.
- `rust/`: reserved for the future Rust crate.
- `PLAN.md`: design and implementation roadmap.

The JavaScript and Rust directories currently document the shared contract;
their libraries are not implemented yet.
