# docs/changes

Working material for the change pipeline (`.claude/skills/change-pipeline`).
One directory per change, named after the branch's slug:

```
docs/changes/<slug>/
├── context.md      # /change-context  — escopo, decisões e invariantes tocadas
├── validation.md   # /change-validate — questões com ID e veredito clean|dirty
├── plan.md         # /change-plan     — itens CI-N, testes e verificação
└── progress.md     # /implement-change — estado, resultados, observações
```

These files are **working material, not the project's record**. What outlives the
change goes to its permanent home:

| O que | Onde |
|---|---|
| Por que uma escolha foi feita | `docs/DECISIONS.md` |
| Como o desenho funciona | `docs/ARCHITECTURE.md` |
| O contrato da API | `docs/openapi.yaml` |
| O que um cliente percebe | `CHANGELOG.md` |
| Uma invariante que passa a valer | `CLAUDE.md` e `.claude/rules/` |

A directory here whose change has shipped can be deleted; nothing reads it after
the PR merges.
