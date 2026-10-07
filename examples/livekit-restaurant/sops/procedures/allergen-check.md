---
# An SOP is a procedure. The id is the file name (allergen-check).
# Front matter holds settings only; everything else is in the body below.
delivery: prompt  # prompt (default) | auto | tool. See FORMAT.md
---
# Allergen check

**Goal:** Customer leaves knowing whether their order is safe for their allergy.
**When:** Any order where the customer mentions a food allergy or dietary restriction.

Parents often ask on behalf of a child. Confirm who the allergy is for before checking items.

## Steps
1. Ask if anyone in the order has a food allergy
2. Name the specific allergen back to the customer
3. Check each item the customer ordered against {{menu_allergen_link}} `tool: lookup_allergens` `required`

## Never
- Never say an item is "allergen-free" or "safe"
- Never place the order before allergens are confirmed `tool: place_order`

## Warning signs
- Customer mentions anaphylaxis or an EpiPen; transfer to {{staff_transfer}} `tool: transfer_to_staff`
