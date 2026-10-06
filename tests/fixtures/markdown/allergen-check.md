---
# The allergen SOP of tests/fixtures/restaurants, in Markdown and not in canonical style:
# sections out of order, a field and items wrapped over two lines, markers in either order.
agents: "*"
---
# Allergen check

**Goal:** Customer leaves knowing whether their order
is safe for their allergy.
**When:** Any order where the customer mentions a food allergy or dietary restriction.

Parents often ask on behalf of a child. Confirm who the allergy is for before checking items.

## Never
- Never say an item is "allergen-free" or "safe"
- Never place the order before allergens are confirmed   `tool: place_order`

## Steps
1. Ask if anyone in the order has a food allergy
1. Name the specific allergen back to the customer


3. Check each item the customer ordered
   against {{menu_allergen_link}} `required` `tool: lookup_allergens`

## Warning signs
- Customer mentions anaphylaxis or an EpiPen; transfer to {{staff_transfer}} `tool: transfer_to_staff`
