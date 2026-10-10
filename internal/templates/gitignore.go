package templates

const goGitignore = `# Binaries and build output
/bin/
*.exe
*.exe~
*.dll
*.so
*.dylib
*.test
*.out
*.a
*.o

# Coverage
coverage.out
coverage.html

# Go workspace
go.work
go.work.sum
`

const nodeGitignore = `# Dependencies
node_modules/
.pnp
.pnp.js

# Build output
dist/
build/
.next/
out/
*.tsbuildinfo

# Environment
.env
.env.local
.env.*.local

# Logs
npm-debug.log*
yarn-debug.log*
yarn-error.log*
pnpm-debug.log*

# Editors / OS
.DS_Store
.idea/
.vscode/
`

const pythonGitignore = `# Byte-compiled / cache
__pycache__/
*.py[cod]
*$py.class

# Distribution / packaging
build/
dist/
*.egg-info/
.eggs/

# Virtual environments
.venv/
venv/
env/

# Test / coverage
.pytest_cache/
.coverage
htmlcov/
.mypy_cache/
.ruff_cache/

# Environment
.env
`

const rustGitignore = `# Build output
/target/
**/*.rs.bk

# Package
*.pdb

# Editors / OS
.idea/
.vscode/
.DS_Store
`
