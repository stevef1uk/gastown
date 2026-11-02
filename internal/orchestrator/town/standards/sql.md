## Town Hall Coding Standards (SQL)

- **Idempotent schema DDL:** Use `CREATE TABLE IF NOT EXISTS` and `CREATE INDEX IF NOT EXISTS` so schema initialization can run safely on every startup or test run.
- **Constraints & types:** Declare explicit `PRIMARY KEY`, `NOT NULL`, `DEFAULT`, and `FOREIGN KEY` constraints matching `SPEC.md` / `architecture.md` column names verbatim.
- **Parameterized queries:** Always use driver placeholders (`?` for SQLite/MySQL, `$1` for Postgres) in application queries—never concatenate or `fmt.Sprintf` user input into SQL strings.
