"use client";
import { PageHeader } from "@/components/shell/page-header";
import { useApp } from "@/components/shell/app-context";
import { RoutingStudio } from "@/components/routing/routing-studio";

export default function RoutingPage() {
  const { environment, notify, merchants } = useApp();
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Routing" description={`Node-based routing graph for selected merchant / ${environment} (demo build, local state only).`} />
      <RoutingStudio merchants={merchants} environment={environment} notify={notify} />
    </div>
  );
}