# 0002: Access only through AWS Systems Manager

**Decision.** The box's security group has no inbound rules. SSH runs inside an SSM session
(`AWS-StartSSHSession`). The public key is pushed with EC2 Instance Connect for 60 seconds on each connection.

**Why.** No open port means no scanning surface and no key distribution. It works through company firewalls
that allow HTTPS.

**Consequences.** The laptop needs the Session Manager plugin, and the box needs outbound HTTPS to AWS. Each
new connection costs about 1–2 s, which ssh connection sharing hides for short commands.
