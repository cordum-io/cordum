import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

const RESOURCE_ID_RE = /^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,127}$/;

/** Validate a URL-sourced resource ID before passing to API hooks. */
export function isValidResourceId(id: string | undefined): id is string {
  return !!id && RESOURCE_ID_RE.test(id);
}
