import {
  BookOpen,
  FileText,
  Code,
  Terminal,
  Mic,
  Box,
  Send,
  Film,
  Image,
  Database,
  Cpu,
  Bot,
  Wrench,
  Lightbulb,
  Rocket,
  Shield,
  Zap,
  Package,
  GitBranch,
  Workflow,
  Key,
  Globe,
  Activity,
} from 'lucide-svelte';

/**
 * lucide name → component. Guides persist only the name, so this map is the
 * contract between the DB rows and the renderer, and it also drives the icon
 * picker in the editor. Type is inferred so lucide's own component type
 * survives without clashing with Svelte 5's Component<> generic.
 */
export const iconMap = {
  BookOpen,
  FileText,
  Code,
  Terminal,
  Mic,
  Box,
  Send,
  Film,
  Image,
  Database,
  Cpu,
  Bot,
  Wrench,
  Lightbulb,
  Rocket,
  Shield,
  Zap,
  Package,
  GitBranch,
  Workflow,
  Key,
  Globe,
  Activity,
};

export type GuideIconName = keyof typeof iconMap;

export const iconNames = Object.keys(iconMap) as GuideIconName[];

/** Unknown names degrade to BookOpen rather than rendering nothing. */
export function iconFor(name: string) {
  return iconMap[name as GuideIconName] ?? BookOpen;
}
