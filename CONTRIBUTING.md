# Contributing to protolift

protolift is created and maintained by Prabhudas Avanigadda (Proto.Engineer).

## No outside code contributions

This repository does **not** accept code from outside contributors. Pull requests from anyone other
than the maintainer are closed automatically, without review. This keeps the copyright and the
direction of the project with one owner.

You are very welcome to:

- **report a bug or ask for a feature**: open an issue,
- **report a security problem**: privately, see [SECURITY.md](SECURITY.md),
- **fork it** under the Apache License 2.0. Give your fork a different name (see [TRADEMARKS.md](TRADEMARKS.md)).

## For the maintainer

```sh
go vet ./...
go test ./...
go build ./cmd/protolift
```

Every Go file starts with:

```go
// Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
// SPDX-License-Identifier: Apache-2.0
```
