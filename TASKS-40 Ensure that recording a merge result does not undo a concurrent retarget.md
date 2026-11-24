# TASKS-40: Ensure that recording a merge result does not undo a concurrent retarget

Status: merged

## Description

Agent finding:

> **Merge-result recording can undo a concurrent retarget.** The status update reads the ticket, then takes a later metadata snapshot for its write. It can write the old `Topic:` back over a newly committed one.
>
> Use the same metadata revision for the read and the conditional write.

FWIW, I don't think that these concurrent update scenarios are very likely, but if the fix is cheap enough, I see no reason why not to make things more robust.
