# Research Report on Synthesizing a Plausible Git History from a Final Code Snapshot

## The problem is feasible, but it is not a reconstruction problem in the strict sense

What you want to build is best framed as **history synthesis**, not literal history recovery. Git stores commits as immutable objects that point to a tree snapshot and zero or more parents, and Git’s own plumbing lets you create commits directly from tree objects or stream them in with explicit author, committer, dates, messages, parents, and file modifications. At the same time, Git explicitly supports local history rewriting, including reordering, splitting, squashing, dropping, and amending commits before publication. The practical consequence is that a final directory does **not** determine a unique commit history; many different histories can lead to the same final tree, so your system should optimize for a **plausible**, internally coherent, reviewable history rather than pretend to infer the one true past. citeturn18view17turn20view0turn20view1turn20view2turn20view3turn28view0

Software evolution research also supports that framing. Lehman’s work established software evolution as a long-running process of continual change, and later maintenance-and-evolution roadmaps emphasize that change is intrinsic to software and that the first version often appears before the feature set is complete, while much of the architecture survives across later evolution. That is almost exactly the shape you want: an initial commit that is incomplete but architecturally meaningful, followed by a sequence of commits that add capabilities without violating architectural integrity. citeturn18view19turn30view1

From that perspective, the core design question is not “How do I reverse history?” but “How do I generate a **credible staged development narrative** whose terminal snapshot is byte-identical to the input directory?” A strong answer usually optimizes six things at once: end-state fidelity, build/test validity at checkpoints, semantic atomicity of each commit, realistic ordering of features and refactorings, readable commit messages, and cheap replay into a Git repository. That target is an inference from Git’s storage model plus the software-evolution literature rather than a claim made verbatim by any single paper. citeturn18view17turn20view3turn28view0turn30view1

## The most relevant research threads are already mature

The first thread you should lean on is **fine-grained change extraction**. ChangeDistiller represents consecutive AST revisions as generic trees, computes tree edit operations between them, and classifies source-code changes using a change taxonomy. GumTree pushed that line further by focusing on fine-grained AST edit scripts that include move actions and aim to be short and close to developer intent. RefactoringMiner then made refactoring detection in commit history practical at high accuracy, reporting roughly 98% precision and 87% recall on its evaluation oracle. For your product, these three ideas are central because they let you reason about changes as **semantic edits** instead of raw line diffs. citeturn26view0turn25search17turn25search19turn21view2turn21view3

The second thread is **commit untangling and change decomposition**. Research on tangled commits shows that developers often bundle unrelated work into one commit, making review, reversion, and integration harder and making historical analysis less reliable. Follow-up work used program slicing to decompose composite commits, arguing that related changes often affect the same slice of a system; more recent ML work like UTango also treats commit untangling as a graph-and-context problem. This matters because your synthesized history should prefer **atomic commits** with one dominant purpose, and the untangling literature gives you both the goal and candidate mechanisms for partitioning a large final delta into coherent pieces. citeturn18view3turn18view4turn2search14

The third thread is **repository-level code understanding**. Classic feature-location work treats concept location as a software-maintenance task and combines information retrieval with structural dependencies to map natural-language concepts to code. Program-dependence-graph and system-dependence-graph research gives you formal machinery for representing data and control dependencies, while code property graphs merge several classic representations into one graph-oriented substrate. In a history-synthesis system, this is what lets you identify candidate “features,” estimate which files must co-evolve, and detect which code can plausibly appear early versus only after certain abstractions exist. citeturn22view5turn22view4turn22view2turn22view1turn22view3

The fourth thread is **LLM-driven repository editing**. CodePlan frames repository-level coding as a planning problem, using incremental dependency analysis, may-impact analysis, and adaptive planning because repo-wide tasks do not fit in one prompt and involve interdependent multi-file edits. RepoCoder shows that iterative retrieval-generation improves repository-level completion by re-querying the repository using the model’s previous prediction. RepoFusion argues for training models to understand repository context, and Repoformer shows that retrieval should be selective rather than always-on, reporting significant latency reductions without hurting performance. Together, these papers strongly suggest that your app should be a **planner with retrieval and validation**, not a single monolithic prompt. citeturn19view0turn19view1turn18view8turn18view9turn27view0

The fifth thread is **test-guided repair and agentic validation**. Prophet learns a probabilistic model of correct code from successful human patches, ranks candidate patches, and validates them against tests. AlphaRepair reframes repair as predicting the correct code from contextual information rather than learning explicit edit templates. SWE-agent shows that interface design matters: giving the model an agent-computer interface that can navigate repositories, edit files, and run tests materially improves end-to-end software-engineering performance. SWE-bench then gives you a realistic benchmark style for repo-level edits under execution-based evaluation. For your app, that translates into a simple principle: every synthesized milestone should be generated under a **generate–execute–reflect–refine** loop. citeturn18view10turn19view7turn19view2turn19view3turn19view8

## A strong system architecture is neuro-symbolic, not pure prompting

A good implementation starts with a **multi-representation ingest layer**. Parse the repository with language-aware parsers where possible; Tree-sitter is useful here because it is incremental, multi-language, and robust enough to produce useful syntax trees even under imperfect conditions. On top of syntax trees, build symbol tables and import/include graphs, then derive richer dependence structures where your language stack supports them. PDGs and SDGs make data and control dependencies explicit, while CPG-style representations are attractive when you want one graph that unifies multiple static-analysis views. The output of this layer should be a repository model containing files, symbols, dependencies, call edges, public APIs, tests, configuration, and inferred subsystems. citeturn23view0turn22view2turn22view1turn22view3

The next layer should be **feature and milestone discovery**. Here, combine structural signals with concept-location signals. Classic feature-location work shows that software-maintenance tasks benefit from mapping natural-language concepts to source regions and then following dependencies outward; in your use case, the “concepts” are candidate user-visible features, vertical slices, infrastructural subsystems, and cross-cutting concerns such as logging, auth, persistence, or CI. The result should not yet be commits. It should be a graph of candidate milestones with prerequisites and affected files. citeturn22view5turn22view4

Only after that should you attempt **history planning**. The planner should decide what belongs in the seed commit, what belongs in early scaffolding commits, what belongs in feature commits, and which changes should be delayed as refactorings or cleanup. CodePlan is very relevant here because it treats repository-wide editing as a multi-step plan grounded in dependency analysis and impact analysis rather than one-shot generation. Bennett and Rajlich’s staged view of evolution suggests a particularly natural progression: initial development creates a runnable or at least coherent architectural base; later evolution adapts and extends; later servicing adds smaller fixes and polish. In practice, that means your first commits should privilege repository skeleton, package layout, public abstractions, trivial implementations, docs, examples, and smoke tests over fully optimized internals. citeturn19view0turn19view1turn30view1

At execution time, the engine should use a **hybrid of deterministic and generative edits**. Deterministic edits are best for renames, API migrations, mechanical file moves, import updates, generated code, and other transformations that can be represented as AST rewrites or semantic patches. Coccinelle’s semantic-patch model is the clearest demonstration of how patch-like rules can encode generic transformations across many code sites, and work on refactoring-aware merge/integration shows why inverting and replaying refactorings can preserve behavior while realigning edits. Use LLMs where ambiguity is high: choosing minimal earlier implementations, inventing stubs, backfilling tests, decomposing large end-state files into earlier simpler versions, or writing explanatory commit messages. citeturn24view0turn22view13turn22view14

The final layer is **Git emission**. Do not build history with slow shell loops around `git add` and `git commit` unless the repositories are small. Git’s plumbing gives you lower-level tools that are a better fit for synthetic-history generation. `git write-tree` materializes a tree object from the index, `git commit-tree` creates a commit object from a tree plus parents, and `git fast-import` can stream an entire synthetic history with explicit metadata, messages, and parentage directly into packfiles. That makes it easy to replay hundreds or thousands of commits efficiently once your planner has decided what the snapshots and metadata should be. citeturn18view18turn18view17turn18view16turn20view3

## The recommended synthesis loop should be milestone-first and commit-second

The most important practical choice is to synthesize **milestones first**, then split each milestone into commits. If you try to generate hundreds of commits directly from the final repository, you will spend most of your budget on local incoherence. The literature points toward a more reliable loop:

1. **Normalize and model the input repository.** Parse source, discover build and test commands, inventory generated files, detect vendored code, and isolate binary or machine-generated artifacts that should usually appear late or be omitted from intermediate evolution if they are reproducible. Tree-sitter-style parsing, repository mining utilities, and feature-location techniques are the right building blocks here. citeturn23view0turn22view12turn22view4turn22view5

2. **Construct a dependency-aware feature graph.** Use imports, calls, inheritance, data flow, and config usage to identify subsystems and prerequisites. PDGs, SDGs, and CPG-style representations help determine what must exist before what. A useful output here is a set of milestone nodes such as “repo initializes,” “core domain model exists,” “storage abstraction exists,” “CLI/API shell exists,” “tests exist,” “feature X becomes real,” “refactor Y happens,” and “docs/examples mature.” citeturn22view2turn22view1turn22view3

3. **Estimate the seed commit.** Research on software evolution suggests that the first running version may lack many later features while preserving the architecture that survives across the project’s life. So the seed commit should usually contain directory layout, package names, root README, build files, top-level config, empty or stubbed interfaces, placeholder tests or smoke tests, and minimal no-op implementations that make later commits feel additive rather than magical. This is an inference guided by staged software-evolution models, not a direct prescription from one tool paper. citeturn30view1turn18view19

4. **Generate milestone deltas using mixed strategies.** For each milestone, prefer deterministic transforms when you can express the change mechanically, and use LLM generation when you need to imagine a plausible earlier version. For “remove feature backward in time” operations, AST differencing and refactoring detection are especially useful because they identify moves, renames, extracts, and structural edits that should be inverted before you simplify logic. That avoids many impossible backward edits where line-based deletion alone would break the design. citeturn26view0turn25search17turn25search19turn21view2

5. **Split each milestone into atomic commits.** Use semantic clustering informed by the untangling literature: one commit should have one dominant intent, and large multi-file edits should be decomposed by concept, slice, or refactoring boundary. If a milestone affects many files because of one API or naming shift, that can still be one commit. If it mixes a feature, a cleanup, and a test reorganization, split it. citeturn18view3turn18view4turn2search14

6. **Validate after each commit and after each milestone.** The minimum validator is syntax parsing plus buildability. A better validator also runs fast tests, targeted tests chosen by impact, and periodic full regression suites. When no tests exist, create characterization tests first so intermediate snapshots have behavioral anchors; if the language stack allows it, use automated test generation to improve coverage. Mutation testing is particularly useful at milestone boundaries because it tells you whether passing tests are actually discriminating enough to support back-in-time transformations. citeturn22view6turn22view7turn22view8turn22view9turn15search1turn15search5

7. **Reflect, repair, and continue.** If validation fails, do not immediately ask the model to “fix the repo.” Ask it to classify the failure: missing prerequisite, over-aggressive rollback, incorrect stub, misplaced refactor inversion, or insufficient tests. Self-Refine and Reflexion are useful templates here because they show that iterative feedback and episodic memory improve performance over one-shot generation. In practice, store a compact failure memory per repository and per milestone so the agent learns local invariants as it goes. citeturn19view4turn19view6

Several conditions are worth hard-coding. If the parser cannot build a stable AST, fall back to text-level segmentation for that file while keeping semantic methods for the rest of the repository. If retrieval is obviously unnecessary for a narrowly local edit, skip it; selective-retrieval work shows that always retrieving cross-file context can be wasteful or harmful. If a generated commit changes too many unrelated concepts, re-run the splitter before validation instead of after failure. If full regression is too expensive per commit, run cheap syntax/build/impacted-test checks per commit and full tests per milestone. Those policies are implementation recommendations derived from the cited work. citeturn27view0turn19view0turn19view1turn18view8

## The prompts should enforce planning, structure, and self-critique

The core prompting mistake to avoid is asking the model for “the previous version of the repo.” Repository-level code work consistently shows that multi-file tasks need planning and repository context, while Self-Refine and Reflexion show the value of iterative feedback. Structured Outputs then gives you a practical way to enforce machine-checkable schemas rather than free-form prose. The right pattern is to ask the model for **typed intermediate artifacts**, not for raw code first. citeturn19view0turn18view8turn19view4turn19view6turn22view16

A useful first artifact is a **repository map**. In plain English: ask the model to summarize the codebase in terms of subsystems, public entry points, persistent data models, configuration surfaces, tests, generated artifacts, and likely user-visible features, while also listing “must-exist-before” relations. The output should be JSON with small bounded arrays and explicit confidence scores. Structured schema adherence matters here because downstream planners should consume the output automatically instead of trying to scrape prose. citeturn22view16turn22view4turn22view5

```json
{
  "subsystems": [
    {
      "name": "storage",
      "kind": "infra",
      "files": ["src/storage.py", "src/db/config.py"],
      "public_symbols": ["Storage", "connect"],
      "depends_on": ["config"],
      "confidence": 0.94
    }
  ],
  "features": [
    {
      "name": "user authentication",
      "entrypoints": ["src/api/auth.py"],
      "core_files": ["src/domain/user.py", "src/api/auth.py"],
      "prerequisites": ["config", "storage"],
      "tests": ["tests/test_auth.py"],
      "confidence": 0.81
    }
  ],
  "repo_invariants": [
    "build uses poetry",
    "tests require sqlite fixture"
  ]
}
```

That kind of schema is not taken from a paper verbatim, but it is directly motivated by repository-level planning work and by structured-output tooling. citeturn19view0turn22view16

The second artifact should be a **milestone plan** rather than commits. The prompt should explicitly ask for: seed commit, scaffolding milestones, feature milestones, refactor milestones, and polish milestones. For each milestone, require a rationale, prerequisites, expected build/test status, and the specific files that should change. This mirrors CodePlan’s idea that repository-wide edits should be decomposed into a chain of dependent steps, and it gives you an object you can inspect, re-order, or prune before any code is generated. citeturn19view0turn19view1

```json
{
  "milestones": [
    {
      "id": "M0",
      "title": "Initialize repository shell",
      "intent": "seed",
      "prerequisites": [],
      "changes": [
        "add README with project scope",
        "add package layout",
        "add build config",
        "add interface stubs"
      ],
      "expected_validation": {
        "parses": true,
        "builds": true,
        "tests_pass": "partial"
      }
    }
  ]
}
```

The third artifact should be a **commit proposal** for one milestone slice at a time. Ask the model for a patch plan, a minimal intent label, the exact files it expects to touch, whether retrieval is needed, whether a deterministic rewrite is preferable, and what the validation gate should be. RepoCoder and Repoformer both support the idea that retrieval should be iterative and conditional, not unconditional. citeturn18view8turn27view0

```json
{
  "commit_intent": "introduce storage abstraction with no-op implementation",
  "needs_retrieval": true,
  "retrieval_queries": [
    "symbols implementing persistence",
    "tests referencing storage or db"
  ],
  "preferred_edit_mode": "mixed",
  "files": [
    "src/storage.py",
    "src/config.py",
    "tests/test_storage_smoke.py"
  ],
  "backward_constraints": [
    "preserve public Storage interface name",
    "remove concrete sqlite behavior for now"
  ],
  "validation": {
    "parse_only": false,
    "build": true,
    "tests": ["tests/test_storage_smoke.py"]
  }
}
```

The final artifact in the loop should be a **reflection record**. If validation fails, ask for a short diagnosis with one dominant failure class and a bounded repair plan. This is where Self-Refine and Reflexion fit most naturally: store a small episodic memory of what failed for this repository, such as “removing storage config before test fixture migration breaks imports” or “renaming symbol backward requires replaying dependent refactor.” Those memories become high-value local context on subsequent attempts. citeturn19view4turn19view6

Commit-message generation is a separate subproblem, and the literature suggests it should not be treated as pure free-form summarization. ChangeScribe is useful because it generates messages from extracted source-code changes, method roles, commit stereotypes, and impact estimation. In other words, commit messages should be generated from your semantic-change representation after the patch is accepted, not hallucinated before the patch exists. citeturn18view6

## Validation, training data, and evaluation should be designed from real histories

The strongest evaluation design is to take repositories with **real histories**, hide that history, present only the final snapshot to your system, synthesize a history, and then compare your output against both the real past and execution-based criteria. Software Heritage is valuable here because it archives entire source trees together with full development histories captured by modern VCSs, including commit messages that explain why changes were made. PyDriller then gives you a practical mining interface for commits, diffs, metadata, and source. Using those two together, you can build training and evaluation corpora without inventing synthetic benchmarks from scratch. citeturn22view10turn22view12

Your primary metric should be **terminal fidelity**: after replaying the synthetic history, the final tree must match the input repository exactly, ideally byte-for-byte after normalizing generated artifacts that are intentionally excluded. The second metric should be **replay validity**: what fraction of commits parse, build, and pass their intended checkpoints. The third should be **milestone validity**: how often planned major states pass broader tests. Execution-based benchmarks like SWE-bench are relevant here because they operationalize repository editing by whether the resulting code survives the repository’s own validators, not whether a diff “looks right.” citeturn19view8turn15search3turn15search11

The next metric family should measure **historical realism**. Compare your synthetic histories to real ones on commit-size distributions, number of files touched per commit, frequency of pure refactoring commits, ratio of test-only commits, temporal spacing, and message specificity. For message quality, use semantic-change-based generation baselines such as ChangeScribe and newer commit-message-generation literature as references. For semantic similarity between your invented intermediate states and plausible historical candidates, clone-detection resources such as BigCloneBench are relevant because they capture semantically similar code across a wide range of syntactic variation. That can help you score whether a generated earlier implementation looks like a believable human intermediate rather than an obviously back-generated artifact. citeturn18view6turn22view11

You should also evaluate the **strength of your validators**, not just the output histories. Characterization tests are useful when existing tests are weak or absent because they document actual observed behavior rather than idealized intended behavior. Mutation-testing surveys show why this matters: mutation analysis is widely used to assess test-suite adequacy and to guide testing work, and industrial tools such as PIT make this practical on the JVM. In a synthesized-history system, a weak test suite will otherwise let the model generate attractive but semantically nonsensical earlier states. citeturn22view6turn22view9turn15search1turn15search5

There is also a governance issue you should not ignore. Git history rewriting is normal in local workflows, but recent empirical work on altered histories shows that rewritten histories are common at scale and can be used in ways that affect contributor attribution or even retroactively change licensing narratives. If your application is intended for demos, education, archaeology, or onboarding, the safest product stance is to mark the output as **synthetic history** in repository metadata, README text, or signed notes. That makes the tool much easier to defend ethically and operationally. citeturn28view0turn12search2turn12search11

## The most useful papers and materials to read first

If you want the fastest path from theory to implementation, start with the staged software-evolution material by Lehman and Bennett/Rajlich so you internalize what a believable early version should look like and why the architecture often appears before the full feature set. Then read ChangeDistiller, GumTree, and RefactoringMiner together, because they provide the semantic-edit vocabulary your system needs for backward transformations. After that, move to the commit-untangling literature, especially the slicing-based work, because your core generator will be judged on the atomicity and intelligibility of its commits. citeturn18view19turn30view1turn26view0turn25search17turn21view2turn18view3turn18view4

For the LLM side, the best reading order is CodePlan, RepoCoder, RepoFusion, Repoformer, Self-Refine, Reflexion, SWE-agent, and SWE-bench. Read them with one question in mind: what should be symbolic, what should be retrieved, what should be generated, and when should execution feedback enter the loop. The papers are remarkably consistent in pushing away from one-shot generation and toward planning, selective context, external tools, and iterative correction. citeturn19view0turn18view8turn18view9turn27view0turn19view4turn19view6turn19view2turn19view8

On the implementation side, keep Git’s plumbing documentation close at hand. `commit-tree`, `write-tree`, and `fast-import` are not just internals trivia here; they are the cleanest foundation for an app that must emit long synthetic histories efficiently and with reproducible metadata. Pair those docs with Software Heritage and PyDriller for mining and evaluation infrastructure, and with Tree-sitter for a portable parser substrate across languages. citeturn18view17turn18view18turn18view16turn22view10turn22view12turn23view0

My overall technical judgment is that the best version of your product will look less like “LLM writes fake commits” and more like a **history compiler**: a planner that builds a dependency-aware milestone graph, a semantic-edit engine that can invert or split structural changes, an LLM layer that imagines plausible earlier implementations only where ambiguity remains, and an execution loop that refuses to advance when the generated past stops making sense. That conclusion is an inference from the combined evidence above, but it is where the literature most clearly points. citeturn19view0turn18view8turn21view2turn18view3turn19view4turn19view2