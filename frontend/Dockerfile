# Build Stage
FROM node:20-alpine AS builder

WORKDIR /app

# Copy package files
COPY frontend/package.json frontend/package-lock.json ./
RUN npm install

# Copy source code
COPY frontend/ ./

# Build the application
RUN npm run build

# Final Stage (Nginx)
FROM nginx:alpine

# Copy built files from builder
COPY --from=builder /app/dist /usr/share/nginx/html

# Copy Nginx config (optional but recommended for SPA routing)
RUN echo 'server { \
    listen 80; \
    location / { \
        root /usr/share/nginx/html; \
        index index.html; \
        try_files $uri $uri/ /index.html; \
    } \
    location /api { \
        proxy_pass http://backend:8080; \
        proxy_set_header Host $host; \
        proxy_set_header X-Real-IP $remote_addr; \
    } \
    location /ws { \
        proxy_pass http://backend:8080; \
        proxy_http_version 1.1; \
        proxy_set_header Upgrade $http_upgrade; \
        proxy_set_header Connection "upgrade"; \
    } \
}' > /etc/nginx/conf.d/default.conf

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
