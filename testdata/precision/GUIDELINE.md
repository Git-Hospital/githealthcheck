# Labelling a finding

This guideline defines the label each rule's precision is measured against (#67, ADR 0005 C3). A labeller sees one finding at a time: its rule, its repository and commit, its file and line, its message and the code around it. They do not see the other labeller's label, the rule's internals or any count.

A finding is **true** when a reviewer of this code should act on it: change the code, or write down why it stays. It is **false** when it does not hold, or when the code already shows it is meant. Labels are `true` or `false`, each with one sentence saying why.

When the code shown is not enough, read more of the file or the repository at that commit, as each rule below says. A finding that still cannot be judged is labelled false with a reason starting `undecidable:`. A reviewer could not act on it either, and counting it as true would credit the rule with a guess; `score` counts these apart, so a rule that fails for lack of evidence shows it.

Judge the code, not the rule: a finding that matches the rule's page but that no reviewer would change is false. Test files are judged like any other code.

## GHC-CPLX-004: error checked and then dropped

The rule reports `if err != nil {}`, `if err != nil { return nil }` in a function whose last result is `error`, and in Python an `except`, `except Exception` or `except BaseException` whose body is only `pass`, `...` or `continue`.

True: the error is dropped and nothing in the code says why. In a function that returns an error, the caller is told everything went well.

False:
- The checked value is not an error, or the block does something with it that the rule missed, such as passing it on in another return value.
- The code says why, in one of three places: inside the block, on the line directly above the check, or in the function's doc comment (for example "best effort").
- Dropping is plainly the contract of the code around it: a cleanup that must not fail its caller, a probe whose only question is "does this work", an error the next statement checks again.

## GHC-CPLX-006: function nothing references

The rule reports an unexported Go function that no file of its directory names.

Check, at the commit shown: search the function's name as a whole word in every file of its directory (`.go`, `_test.go` and `.s`), and search the repository for `go:linkname` with the name. That is enough; when neither finds a use, the finding is true unless the code says it is kept.

True: nothing uses the function, so deleting it changes nothing.

False:
- The search finds a use the rule missed: assembly, `//go:linkname` from another package, a `go:generate` step or template that writes a call.
- It is kept on purpose and the code says so: a `//nolint:unused` with a reason, or a comment saying it is for a debugger or a coming caller.

## GHC-CPLX-008: function that only says it is not implemented

The rule reports a function whose whole body panics, raises or returns an error saying it is not implemented, or a TODO.

True: a placeholder that someone meant to fill in. Signs: a TODO, "not yet", an issue link, or a sibling implementation of the same method that works, with no platform or backend reason for this one to differ. A placeholder nothing calls is true too: a reviewer should implement it or delete it.

False:
- An operation deliberately not supported here, which callers are expected to handle: the message, the file name (`_windows.go`, `_js.go`) or the doc comment gives a platform or backend that cannot do it, or the type is a read-only implementation of a writable interface.
- A test double or fake whose unused methods panic, so that a test that reaches them fails loudly.
- An abstract method in Python that a subclass must override, when the class is plainly a base class.

When neither list fits, ask whether a user calling the function would be surprised that it fails: if yes, true.
