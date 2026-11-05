# 0008: Budgets enforced on the box, from a laptop ledger

**Decision.** Spend is estimated as running time × list price, from a ledger rebuilt from the cloud's own start
and stop times. Budgets are checked before anything starts (`BUDGET`, exit 9). The box is told the minutes its
remaining budget pays for and stops itself, next to the idle and max-hours stops.

**Why.** Users must be able to cap spend without a server, and with the laptop closed.

**Consequences.** It is an estimate (5-minute stop granularity, no disk or network charges). The UI and
docs say so. A cloud with no known price for a type cannot enforce the budget, and protolift warns instead
of guessing.
