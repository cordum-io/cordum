declare module "node:fs" {
  export function readFileSync(
    path: string | URL,
    options?: string | { encoding?: string; flag?: string },
  ): string;
}

declare module "node:path" {
  export function dirname(path: string): string;
  export function resolve(...paths: string[]): string;
}

declare module "node:url" {
  export function fileURLToPath(url: string | URL): string;
}
