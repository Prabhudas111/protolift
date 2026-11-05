# 0005: Three independent ways a box stops

**Decision.** (1) The box's own timer stops it after `idle_minutes` with no sshd traffic above 64 KB per 5 min
and no open terminal. (2) It always stops at `max_hours`. (3) A CloudWatch alarm stops it after 60 minutes of
CPU under 5%.

**Why.** A forgotten box is the biggest risk to the user's bill. A connected-but-quiet tunnel must not count as
use, or a laptop left open overnight keeps billing. Layer 3 works even if the box script or the laptop fails.

**Consequences.** Long silent jobs (for example a 3-hour build with no one connected) can be stopped by the
idle rule. Raise `idle_minutes` for those.
