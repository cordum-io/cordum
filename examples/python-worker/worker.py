from cordum import Worker, JobContext

worker = Worker(
    pool="hello-pack",
    subjects=["job.hello-pack.echo"],
    capabilities=["hello-pack.echo"],
)


@worker.handler("job.hello-pack.echo")
async def handle_echo(ctx: JobContext):
    message = ctx.input.get("message", "hello from python")
    author = ctx.input.get("author", "")
    return {"message": message, "author": author}


if __name__ == "__main__":
    worker.run()
