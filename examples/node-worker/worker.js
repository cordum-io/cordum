import { Worker } from "@cordum/sdk";

const worker = new Worker({
  pool: "hello-pack",
  subjects: ["job.hello-pack.echo"],
  capabilities: ["hello-pack.echo"],
  natsUrl: process.env.NATS_URL || "nats://localhost:4222",
});

worker.handle("job.hello-pack.echo", async (ctx) => {
  const message = ctx.input?.message ?? "hello from node";
  const author = ctx.input?.author ?? "";
  return { message, author };
});

worker.start();
