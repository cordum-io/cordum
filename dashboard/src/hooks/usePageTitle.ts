import { useEffect } from "react";

const DEFAULT_TITLE = "Cordum Control Plane";

export function usePageTitle(title: string): void {
  useEffect(() => {
    document.title = title ? `${title} | Cordum` : DEFAULT_TITLE;
    return () => {
      document.title = DEFAULT_TITLE;
    };
  }, [title]);
}
