import { useEffect } from "react";

// usePageTitle updates document.title so multi-tab browsing shows which
// page is which. Called at the top of every page component.
export function usePageTitle(title: string) {
  useEffect(() => {
    const prev = document.title;
    document.title = `${title} · FlareX admin`;
    return () => {
      document.title = prev;
    };
  }, [title]);
}
