// Mock versions of the tools the SOPs reference (see "tools" in sops/build/lock.json).
//
// A real deployment would call the POS, menu and reservation systems. These return
// canned answers so the example runs end to end, and log every call so you can see
// which SOP step triggered it.

import { llm } from '@livekit/agents';
import { z } from 'zod';

const log = (name: string, args: unknown, result: unknown) => {
  console.log(`@${Date.now()} [tool] ${name} ${JSON.stringify(args)} -> ${JSON.stringify(result)}`);
  return result;
};

// Pretend allergen data, keyed by words that appear in item names.
const ALLERGENS: Record<string, { contains: string[]; mayContain: string[] }> = {
  taco: { contains: ['corn', 'dairy (crema)'], mayContain: ['peanuts', 'tree nuts', 'sesame'] },
  pizza: { contains: ['wheat', 'dairy'], mayContain: ['tree nuts'] },
  margherita: { contains: ['wheat', 'dairy'], mayContain: ['tree nuts'] },
  sushi: { contains: ['fish', 'soy', 'sesame'], mayContain: ['shellfish'] },
  pasta: { contains: ['wheat', 'egg'], mayContain: ['dairy'] },
};

export const mockTools = {
  lookup_allergens: llm.tool({
    description: 'Look up the allergens for a menu item.',
    parameters: z.object({
      item: z.string().describe('The menu item, e.g. "chicken tacos"'),
      allergen: z.string().optional().describe('The allergen the caller asked about, if any'),
    }),
    execute: async (args) => {
      const key = Object.keys(ALLERGENS).find((k) => args.item.toLowerCase().includes(k));
      const result = key
        ? { item: args.item, ...ALLERGENS[key], note: 'Prepared in a shared kitchen; cross-contact is possible.' }
        : { item: args.item, note: 'No allergen data for this item. Staff must confirm.' };
      return log('lookup_allergens', args, result);
    },
  }),

  transfer_to_staff: llm.tool({
    description: 'Transfer the call to a staff member.',
    parameters: z.object({ reason: z.string().describe('Why the call is being transferred') }),
    execute: async (args) => log('transfer_to_staff', args, { status: 'transferring', eta_seconds: 20 }),
  }),

  place_order: llm.tool({
    description: 'Place the order once everything is confirmed.',
    parameters: z.object({
      items: z.array(z.string()).describe('Items with quantities, e.g. ["3x chicken taco"]'),
      fulfillment: z.enum(['pickup', 'delivery']),
    }),
    execute: async (args) => log('place_order', args, { order_id: 'A-1042', ready_in_minutes: 20 }),
  }),

  check_delivery_zone: llm.tool({
    description: 'Check whether an address is inside the delivery zone.',
    parameters: z.object({ address: z.string() }),
    execute: async (args) =>
      log('check_delivery_zone', args, { in_zone: !/staten island/i.test(args.address), estimated_minutes: 40 }),
  }),

  check_reservations: llm.tool({
    description: 'Check table availability.',
    parameters: z.object({ party_size: z.number(), date: z.string(), time: z.string() }),
    execute: async (args) => log('check_reservations', args, { available: args.party_size <= 6, alternatives: ['7:30pm', '9:00pm'] }),
  }),
};
