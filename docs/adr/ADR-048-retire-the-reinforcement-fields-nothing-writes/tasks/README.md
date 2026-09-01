# ADR-048 tasks

One task. The change is a single coherent removal plus the gate that keeps it removed, and splitting
it would produce a commit where the fields are gone and nothing stops them returning.

| Task | Status | Depends-on | Goal |
|------|--------|------------|------|
| [T1](T1-no-unwritten-dynamics-field-reaches-the-wire.md) | pending | none | Remove the four inert dynamics keys from both wire views, delete the exemption they made dead, and gate their return |
