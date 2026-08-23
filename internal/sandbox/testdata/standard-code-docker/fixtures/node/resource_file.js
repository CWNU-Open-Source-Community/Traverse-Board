"use strict";

const fs = require("fs");
fs.truncateSync("resource-limit.bin", 64 * 1024 * 1024);
throw new Error("workspace single-file limit was not enforced");
