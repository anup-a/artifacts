# byagent's remote MCP server (mcp/server.js). It imports the CLI's API client
# and Markdown renderer, so the build context is the repo root:
#
#   docker build -f mcp/Dockerfile -t byagent-mcp:local .
FROM node:22-alpine
WORKDIR /app
COPY cli/package.json cli/package-lock.json cli/
RUN npm --prefix cli ci --omit=dev --ignore-scripts && npm cache clean --force
COPY cli/src cli/src
COPY mcp/server.js mcp/tools.js mcp/
ENV NODE_ENV=production PORT=8090
EXPOSE 8090
USER node
CMD ["node", "mcp/server.js"]
