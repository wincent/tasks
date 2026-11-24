# TASKS-41: Avoid losing configured signature policies for unchecked-out topics

Status: active

## Description

Agent finding:

> **An unchecked-out topic can lose its configured signature policy.** Merge reads policy from the invoking checkout when the destination has no checkout. Branch-conditional signing requirements can therefore differ depending on whether the topic is checked out.
>
> Resolve policy in the destination's branch context, or refuse the merge if that policy can't be determined.

FWIW, this is an unlikely edge case, but if the fix can be implemented at relatively low cost, we may as well do that.
