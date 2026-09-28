// A single-target credential broker. The agent never receives the upstream
// credential or a route to the public network. It can only call this server.
const http = require('node:http');
const https = require('node:https');
const os = require('node:os');

const endpoint = new URL(process.env.TURNYARD_GATEWAY_ENDPOINT);
const credential = process.env.TURNYARD_GATEWAY_CREDENTIAL;
const placeholder = process.env.TURNYARD_GATEWAY_PLACEHOLDER;
if (endpoint.protocol !== 'https:' || !credential || !placeholder || endpoint.username || endpoint.password) {
  process.exit(2);
}
const basePath = endpoint.pathname.replace(/\/$/, '');
const server = http.createServer((request, response) => {
  if (request.url === '/_turnyard_health') {
    response.writeHead(200).end('ok');
    return;
  }
  if (!request.url.startsWith('/') || request.url.startsWith('//')) {
    response.writeHead(400).end();
    return;
  }
  let incoming;
  try {
    incoming = new URL(request.url, 'http://turnyard-model');
  } catch {
    response.writeHead(400).end();
    return;
  }
  if (incoming.origin !== 'http://turnyard-model' ||
      (basePath && incoming.pathname !== basePath && !incoming.pathname.startsWith(basePath + '/'))) {
    response.writeHead(403).end();
    return;
  }
  const headers = { ...request.headers };
  delete headers.host;
  delete headers.connection;
  delete headers['proxy-authorization'];
  let authenticated = false;
  for (const name of ['authorization', 'x-api-key', 'api-key']) {
    if (typeof headers[name] === 'string' && headers[name].includes(placeholder)) {
      headers[name] = headers[name].replaceAll(placeholder, credential);
      authenticated = true;
    } else if (headers[name] !== undefined) {
      response.writeHead(403).end();
      return;
    }
  }
  if (!authenticated) {
    response.writeHead(401).end();
    return;
  }
  const target = new URL(endpoint.origin);
  target.pathname = incoming.pathname;
  target.search = incoming.search;
  const upstream = https.request(target, {
    method: request.method,
    headers,
    timeout: 120000,
  }, (reply) => {
    response.writeHead(reply.statusCode || 502, reply.headers);
    reply.pipe(response);
  });
  upstream.on('timeout', () => upstream.destroy(new Error('upstream timeout')));
  upstream.on('error', () => {
    if (!response.headersSent) response.writeHead(502);
    response.end();
  });
  request.pipe(upstream);
});
server.on('connect', (_request, socket) => socket.destroy());
// Bind only the internal-network interface. A container on Docker's public
// bridge must not be able to use this invocation's credential broker.
const internalAddress = Object.entries(os.networkInterfaces())
  .filter(([name]) => name !== 'lo')
  .flatMap(([, entries]) => entries)
  .find((entry) => entry.family === 'IPv4' && !entry.internal)?.address;
if (!internalAddress) process.exit(2);
server.listen(8080, internalAddress);
