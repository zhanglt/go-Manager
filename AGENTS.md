# Repository Guidelines

When you reply or any output ,Please translate to chinese.

## Project Structure & Module Organization

This repository builds the NeuVector Security Center manager. The root SBT build contains two Scala 3 modules: `common/` for shared models, caches, utilities, and configuration, and `admin/` for the manager service. Scala sources follow the standard `src/main/scala` and `src/test/scala` layout; runtime data belongs in `src/main/resources`.

The Angular application lives in `admin/webapp/`. Put feature code under `websrc/app/`, static files and translations under `websrc/assets/`, and environment settings under `websrc/environments/`. Python CLI commands are in `cli/prog/`; packaging and image scripts are in `package/`, `scripts/`, and the root `Makefile`.

## Build, Test, and Development Commands

- `sbt compile` compiles the Scala modules; `sbt test` runs all ScalaTest suites.
- `sbt assembly` creates the manager fat JAR (tests are disabled for this task, so run them separately).
- `make jar` performs the containerized JAR build; `make build-image` builds and loads the manager image with Docker Buildx.
- From `admin/webapp`, run `npm ci` for reproducible dependencies, `npm start` for the development server, and `npm run build` for a production UI build.
- `npm run lint:check` and `npm run format:check` validate frontend style without modifying files.

## Coding Style & Naming Conventions

Scala is formatted on compile by Scalafmt: use two-space continuation indentation, a 100-column limit, package paths under `com.neu`, PascalCase types, and camelCase members. Angular/TypeScript uses two spaces, single quotes, semicolons, and an 80-column Prettier limit. Follow Angular filenames such as `registry-details.component.ts`, with colocated HTML, SCSS, and tests. Keep Python modules lowercase with underscores.

## Testing Guidelines

Name Scala suites `*Suite.scala` under the matching module's `src/test/scala`; use ScalaTest. Name Angular tests `*.spec.ts` beside the implementation; Karma runs Jasmine tests through Chrome with `npm test`. Add focused regression coverage for behavioral changes. REST integration fixtures and the shell runner are under `admin/test/REST_API/`.

## Commit & Pull Request Guidelines

Recent history favors short, imperative subjects, often using `feat:`, `fix:`, or `chore:`; issue-prefixed subjects such as `#1246 [bug] ...` also appear. Keep each commit scoped to one concern. Pull requests should explain behavior and validation, link the relevant issue, and include screenshots for UI changes. Note configuration, API, dependency, or image-build impacts, and request review from the owners in `CODEOWNERS`.

## Security & Configuration

Do not commit credentials, tokens, generated build output, or local environment overrides. Report vulnerabilities through `SECURITY.md`, not public issues.
