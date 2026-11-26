import { NextResponse } from "next/server";
import { directoryIndex } from "../../../../lib/directoryAccess.js";

export const dynamic = "force-static";

// Public discovery endpoint for the room directory. It contains no key values,
// no owner passphrase, and no transcript: only ids, sections, and how each
// contact joins.
export async function GET() {
  return NextResponse.json(directoryIndex(), {
    headers: { "Cache-Control": "public, max-age=300, s-maxage=3600" },
  });
}
