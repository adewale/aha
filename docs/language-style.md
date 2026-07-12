# Language style

Aha uses **British English** throughout first-party prose and human-facing interfaces.

Preferred spellings include:

- analyse, behaviour, catalogue, centre, colour;
- initialise, optimise, organise, recognise, sanitise;
- authorisation, licence (noun), artefact;
- modelling, labelled, cancelled.

This applies to:

- documentation and comments;
- CLI help, progress, errors, hints, and next actions;
- dashboard and MCP descriptions;
- first-party domain terminology and newly introduced identifiers;
- generated first-party documentation.

Required external tokens retain their specified spelling. Examples include:

- HTTP's `Authorization` header and `unauthorized` machine code;
- MCP's `initialize` and `notifications/initialized` methods;
- CSS properties such as `color`, `text-align: center`, and `scroll-behavior`;
- AWS/Cloudflare error codes and third-party API field names;
- SPDX metadata, the `LICENSE` filename, source URLs, quoted product names, and cited source text;
- immutable legacy schema/key/ref strings such as `catalog/v1`, `artifacts`, and `artifact:v1` until the 0.2 schema replacement removes or versions them deliberately.

External spellings are protocol compatibility, not house style. They should be isolated at boundaries rather than copied into surrounding user-facing prose.

The 0.2 command/state redesign must review public JSON names and persisted schema vocabulary explicitly. Because aha is pre-launch, first-party American-English identifiers need no compatibility alias when their 0.2 replacements land.
