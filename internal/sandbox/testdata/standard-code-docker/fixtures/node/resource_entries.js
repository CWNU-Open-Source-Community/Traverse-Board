"use strict";

const fs = require("fs");
for (let index = 0; index < 10_000; index += 1) {
  fs.writeFileSync(`resource-entry-${index}`, "");
}
throw new Error("workspace entry-growth limit was not enforced during execution");
