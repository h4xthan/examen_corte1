FROM node:20-alpine AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install
COPY . .
RUN npm run build

FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
# nginx.conf hace `include /etc/nginx/conf.d/security-headers.conf`, que es donde
# deben vivir las cabeceras: dentro de un include se pueden usar directivas
# justo como en el bloque padre.
COPY security-headers.conf /etc/nginx/conf.d/security-headers.conf
EXPOSE 80