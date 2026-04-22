import { createServer } from "vite";
import { fileURLToPath } from "node:url";

export default async function globalSetup(): Promise<() => Promise<void>> {
  const root = fileURLToPath(new URL(".", import.meta.url));
  const server = await createServer({
    root,
    configFile: fileURLToPath(new URL("./vite.config.ts", import.meta.url)),
    server: {
      host: "127.0.0.1",
      port: 4173,
    },
  });

  await server.listen();

  return async () => {
    await server.close();
  };
}
