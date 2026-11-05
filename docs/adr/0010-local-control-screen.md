# 0010: A local control screen, loopback only

**Decision.** `protolift ui` serves an embedded page on 127.0.0.1 with a random token in the link. Every
request is checked for the token (constant time) and a loopback Host header, which blocks DNS rebinding. The page
uses a strict CSP and loads nothing external.

**Why.** Budgets and stops are easier to see and change on a screen. Nothing may be reachable from the
network or from other websites.
