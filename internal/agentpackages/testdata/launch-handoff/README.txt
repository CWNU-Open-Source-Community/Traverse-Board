Controlled Agent Plugins 1.0.0 interoperability fixture, authored for tests.
This is not an upstream real-world plugin and contains no executable or script.
The pinned real plugin/Skill samples under ../upstream remain unchanged.

A's OpenDirectory -> Launches must produce six untouched declarations and one
local diagnostic for invalid-unknown-cwd. Missing helper.exe is intentional:
A does not resolve, authorize, or execute launches. A's public fixture test is
TestPortableLaunchHandoffPreservesResolverInputs. All shared values use the
reviewed internal/toolcontract, without another test/runtime contract.

B should COPY this directory into a controlled install root, load it through
agentpackages.OpenDirectory, then feed the actual returned declarations into its
resolver with trusted LaunchContext. Do not construct equivalent declarations
by hand; that misses the conversion seam. Identify cases via ServerSource.
Create bin/helper.exe from B's own known test helper only if resolution requires
an existing executable. There is no need to execute it or make an HTTP request
to verify these resolution cases. Test data roots and work directories are owned
by the test. C still owns all runtime authority checks for actual dispatch.

Case                         Expected B behavior for Format=agent-plugins/1.0.0
command-versus-data-cwd       command = installRoot/bin/helper.exe;
                             cwd = dataRoot/work, never dataRoot/work/bin/helper.exe.
command-versus-plugin-cwd     command = installRoot/bin/helper.exe;
                             cwd = installRoot/work.
default-cwd                  cwd = installRoot; do not inherit host process cwd.
http-literals                Endpoint and headers equal the original strings;
                             do not expand any variables in either.
escape-command               Reject the command escaping installRoot.
escape-data-cwd              Reject cwd escaping dataRoot, even if the target
                             exists under some other trusted directory.
invalid-unknown-cwd           A reports/skips this entry: unsupported cwd form.

In args/env only, replace PLUGIN_ROOT and PLUGIN_DATA once. UNKNOWN, $HOME and
%TEMP% remain literal. Repeat with installRoot's actual directory basename
containing the literal text ${PLUGIN_DATA}; its introduced text must not be
expanded recursively. Read environment fields as data, not shell syntax.

Additional resolver negatives should create contained then escaping real links
under the fixture's ./bin or working-directory paths. Both command and cwd must
be checked against their own filesystem-resolved root. A content digest alone
does not prove that a path stays within that root. Windows environment names
need platform-aware merging so case variants cannot replace host-owned roots.

Normative references: https://agent-plugins.org/specification sections 4.1,
7.2.1, 7.2.2, 9.1 and 9.2. These rules apply to the Agent Plugins format;
do not impose them on unrelated ecosystem-native formats without their own
versioned adapter. A passing loader test does not prove B's resolver passes.
