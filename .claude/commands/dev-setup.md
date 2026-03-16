# Dev Setup

Help set up the development environment for ServerPanel.

## Steps
1. Check if Go 1.22+ is installed
2. Check if Node.js 18+ and npm are installed
3. Check if MongoDB is running (or Docker is available)
4. Run `make setup` to install all dependencies
5. Verify `.env` file exists (copy from `.env.example` if not)
6. Start development servers with `make dev`

## Troubleshooting
- If MongoDB connection fails, check `MONGO_URI` in `.env`
- If frontend fails, try `cd frontend && npm install` manually
- If Air hot-reload fails, install with `go install github.com/air-verse/air@latest`
