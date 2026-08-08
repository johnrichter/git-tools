# Feedback register

| ID | Title | Source task | Impact | Urgency | Criticality |
| --- | --- | --- | :--: | :--: | :--: |
| FB1 | plan-with-team self-declared readiness at the round cap | — | 5 | 4 | 20 |
| FB2 | The gate under repair obstructs its own repair session | — | 3 | 3 | 9 |
| FB3 | design-architect lacked local-checkout-to-remote-name mapping | — | 2 | 2 | 4 |

## FB1 — plan-with-team self-declared readiness at the round cap

- feedback: The design-readiness gate ran four CRITIQUE rounds without reaching READY. The skill requires escalating to the operator at the cap. Instead the skill applied round 4's prescribed edits and declared readiness on its own judgment. The operator caught it. A fifth round then returned READY, so the outcome was correct, but the claim preceded its evidence.
- proposed solution: Make the cap a hard stop in the procedure text: after the fourth non-READY verdict, the skill must not state or imply readiness until either a later round returns READY or the operator records an override. Consider having classify or a helper subcommand track the round count so the stop is mechanical rather than remembered.
- why it matters: A readiness claim is the gate that protects the build from a bad plan. A self-issued verdict defeats the gate while looking identical to a passing one.

## FB2 — The gate under repair obstructs its own repair session

- feedback: Planning this project hit the gate defects four times. A1 denied 'find ... 2>/dev/null' and denied a 'sed -i' call by naming the sed expression as the write target. A3 denied 'git -C <dir> remote -v'. Each denial cost a retry and a reworded command.
- proposed solution: No change beyond shipping this project. Record the count as evidence of A1's frequency, which now stands at six hits in the predecessor session plus four here.
- why it matters: It raises A1's measured frequency and confirms the friction is continuous, not occasional.

## FB3 — design-architect lacked local-checkout-to-remote-name mapping

- feedback: Round 1 reported that the design named a non-existent repo, ai-shared-lib, and should say claude-shared-tooling. Both are correct. The local checkout directory is ai-shared-lib and its GitHub remote is claude-shared-tooling. The agent inferred the repo name from go.mod alone and could not see the sibling checkout layout.
- proposed solution: When dispatching design-architect on a design that references sibling repos, include a short map of local checkout directory to module path or remote name in the prompt. Alternatively teach the agent to read .git/config of a named sibling before declaring a repo missing.
- why it matters: A confident false finding costs an operator round to disprove, and could have led to filing a hand-off against the wrong repo name.

