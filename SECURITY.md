# Security

protolift creates resources in **your own** AWS account with **your own** AWS login.
It runs no server of its own, collects no data and needs no account with us.

What protolift does on purpose:

- The cloud box has **no inbound network rules**. All access goes through AWS Systems Manager.
- SSH keys are single-use: a new key is pushed for 60 seconds for each connection and never stored on the box.
- The disk is encrypted. IMDSv2 is required.
- `.env` files are copied laptop → box only, with owner-only permissions, and are never printed.
- Every resource is tagged `protolift:*`, and `protolift down` / `protolift uninstall` remove them.

## Reporting a problem

Please do **not** open a public issue for a security problem.
Use GitHub's private report form: **Security → Report a vulnerability** on
https://github.com/Prabhudas111/protolift.

We aim to reply within 3 working days.
