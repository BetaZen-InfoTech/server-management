FROM node:20-alpine
WORKDIR /app
COPY package.json package-lock.json* ./
COPY apps/whm/package.json ./apps/whm/
COPY apps/cpanel/package.json ./apps/cpanel/
COPY packages/ui/package.json ./packages/ui/
COPY packages/api-client/package.json ./packages/api-client/
COPY packages/types/package.json ./packages/types/
COPY packages/tailwind-config/package.json ./packages/tailwind-config/
RUN npm install
COPY . .
EXPOSE 5173 5174
CMD ["npm", "run", "dev"]
