#!/usr/bin/env node
// Regenerates docs/GUILD-DIRECTORY.md from web/dev-guild/data/contacts.json.
// Run this after adding or editing a directory contact.
import { writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { renderDirectoryMarkdown } from "../web/dev-guild/lib/directoryAccess.js";

const target = fileURLToPath(new URL("../docs/GUILD-DIRECTORY.md", import.meta.url));
writeFileSync(target, renderDirectoryMarkdown());
console.log(`wrote ${target}`);
