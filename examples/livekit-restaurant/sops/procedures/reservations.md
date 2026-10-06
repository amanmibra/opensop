---
# Targets two agents by id. Use the platform ref ("vapi:asst_...") if you prefer.
agents: [sakura-sushi, luigis-trattoria]
---
# Reservations

**Goal:** The customer has a confirmed table, or knows exactly why one isn't available.
**When:** The customer wants to book, change or cancel a table.

## Steps
1. Ask for party size, date and time
2. Check availability `tool: check_reservations`
3. Confirm the booking details and the name on the reservation

## Never
- Never double-book a table
