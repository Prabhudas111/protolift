# Contributing to protolift

Thank you for helping. protolift is created and maintained by
Prabhudas Avanigadda (Proto.Engineer).

## Before you send a change

1. Open an issue first for anything bigger than a small fix, so we can agree on the approach.
2. Keep one change per pull request.
3. Run the checks:

   ```sh
   go vet ./...
   go test ./...
   go build ./cmd/protolift
   ```

4. Every new Go file starts with the copyright header:

   ```go
   // Copyright 2026 Prabhudas Avanigadda (Proto.Engineer)
   // SPDX-License-Identifier: Apache-2.0
   ```

## Your rights and ours

By sending a contribution (a pull request, a patch or a suggestion with code), you agree that:

1. You wrote it, or you have the right to submit it.
2. It is licensed to everyone under the Apache License 2.0, the same as the rest of the project.
3. You also give Prabhudas Avanigadda a permanent, worldwide, royalty-free right to use,
   change, relicense and distribute your contribution as part of protolift or any product
   built from it, including under other license terms.

You keep the copyright in your own contribution. Add a `Signed-off-by:` line to each
commit (`git commit -s`) to confirm the points above.
