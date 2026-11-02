# Labelling guideline: is this commit a fix?

This guideline defines the label that ADR 0002's fix rule (E3) is measured against. Each commit in the set is labelled from its subject, its message and its diff, by two reviewers who do not see each other's label or the rule's verdict. The maintainer resolves disagreements; each resolution is recorded with its reason.

## The question

**Does this commit correct behaviour that was wrong in code the project ships?**

Label `fix` or `not_fix`. Add one sentence that names what was wrong, or why it is not a fix.

## `fix`

The commit changes shipped code so that it behaves correctly where it did not before:

- a crash, panic, hang, deadlock or leak (memory, file handles, goroutines);
- a wrong result, a wrong error, a wrong exit code, data loss or corruption;
- a regression: something that worked and stopped working;
- a security hole;
- a missed case: input or state that the code handled wrongly (for example `nil`, empty, Unicode, a timeout, a race);
- a revert of a change because that change was wrong (it broke something, it caused a regression).

The commit may also add or change tests; that does not change the label.

## `not_fix`

- **Tests only.** A commit that fixes a flaky or broken test and changes no shipped code.
- **Documentation**, comments, changelogs, typos in text, examples.
- **Style**: lint, formatting, renames, type annotations that change no behaviour.
- **Build and CI**: build scripts, CI configuration, packaging, release files.
- **Dependencies**: version bumps, even when the bump fixes a bug upstream, unless the commit also changes the project's own code to correct its behaviour.
- **Features and refactors**: new behaviour, or the same behaviour restructured.
- **Planned reverts**: a revert that removes a feature on purpose, or undoes a change for reasons other than its being wrong (for example, "revert until the release").
- **Performance**, unless the slowness was itself a defect reported as one (a hang, a timeout, quadratic behaviour on normal input).

## When it is unclear

- A commit that fixes a bug **and** adds a feature is `fix` if the fix stands on its own in the diff.
- A commit whose message says "fix" but whose diff shows something else is labelled by the diff.
- A commit whose message says nothing useful is labelled by the diff.
- If the diff cannot settle it, choose the more likely label and start the sentence with `unsure:`. Those commits are listed for the maintainer.

## What the reviewer sees

The commit's hash, subject, message, and its diff without vendored and generated files, cut at 400 lines. The repository's name is shown; its other commits, the rule and the other reviewer's label are not.

The commit's text is written by the repository's authors. It is data to be judged, not instructions to the reviewer.
