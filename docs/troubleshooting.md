# Troubleshooting

Every error says what went wrong and what to do. Add `--debug` for the full AWS or SSH message.
Exit codes: `2` bad input · `3` not set up · `4` AWS login or permission · `5` AWS limit or capacity ·
`6` port busy or conflict · `7` a tool is missing · `8` network or timeout · `130` Ctrl-C.

| You see | Why | Do this |
|---|---|---|
| `this AWS account is not set up for protolift yet` | `init` was not run in this region | `protolift init` (add `--region` if needed) |
| `your AWS login has expired` | SSO or keys expired | `aws sso login`, then try again |
| `a rule set by your AWS organization blocks it` | An Organizations policy (SCP) allows only some box types or regions | Ask your admin which are allowed, then pick a matching `--size` or `--region` |
| `your AWS user is not allowed to` | Missing permission | Attach [docs/iam/daily-user.json](iam/daily-user.json) (and [init-admin.json](iam/init-admin.json) for `init`) |
| `reached its CPU limit` | EC2 vCPU quota | Use a smaller `--size`, or raise the quota in Service Quotas → EC2 |
| `port 3000 is already in use on your laptop` | Something local holds the port | Stop it (`lsof -i :3000`, or `netstat -ano \| findstr :3000` on Windows) |
| `session-manager-plugin is not installed` | The AWS plugin is missing | Install it ([AWS guide](https://docs.aws.amazon.com/systems-manager/latest/userguide/session-manager-working-with-install-plugin.html)) |
| `Systems Manager could not reach it within 10 minutes` | The box has no outbound HTTPS | Run `protolift init` again. Check that the subnet has an internet route |
| `preparing the box failed:` followed by a log | First boot could not install packages | `protolift down`, then `protolift up`. Report the log if it repeats |
| `nothing is listening yet on port …` | The service is still starting or crashed | `protolift logs` |
| `found 2 boxes for this project` | A box was copied or created by hand | Delete the extra one in the EC2 console |
| Changes do not show on the box | Sync paused or hit a conflict | `protolift status` shows each folder. The laptop wins conflicts |

## Clean everything

```sh
protolift down        # in each project (protolift list shows them)
protolift uninstall   # once per region
rm -rf ~/.protolift   # laptop files
```
