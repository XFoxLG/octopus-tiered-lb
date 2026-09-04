'use client';

import * as React from 'react';
import { Info } from 'lucide-react';

import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/animate-ui/components/animate/tooltip';
import { cn } from '@/lib/utils';

type HintProps = {
  /** 悬浮显示的提示文案，为空时整个组件不渲染 */
  text?: string;
  /** 提示弹出方向，默认 top */
  side?: 'top' | 'bottom' | 'left' | 'right';
  className?: string;
  /** 传入时图标作为该元素的悬浮触发器（asChild，不产生额外 DOM） */
  children?: React.ReactElement;
};

/**
 * 表单提示收纳组件：小 i 图标 + 悬浮动画 tooltip。
 * 传 children 时图标直接作为 children 的悬浮触发器（不产生额外 DOM）；
 * 不传 children 时渲染独立图标，放在 label 文字后。
 */
function Hint({ text, side = 'top', className, children }: HintProps) {
  if (!text) return null;

  const icon = (
    <Info
      className="size-3.5 text-muted-foreground"
      strokeWidth={2}
      aria-hidden="true"
    />
  );

  const triggerClass = cn(
    'inline-flex cursor-help items-center rounded-sm p-0.5 align-middle text-muted-foreground outline-none focus-visible:ring-[3px] focus-visible:ring-ring/50',
    className,
  );

  return (
    <Tooltip side={side}>
      {children ? (
        <TooltipTrigger asChild tabIndex={0} className={triggerClass}>
          {children}
        </TooltipTrigger>
      ) : (
        <TooltipTrigger tabIndex={0} className={triggerClass}>
          {icon}
        </TooltipTrigger>
      )}
      <TooltipContent className="max-w-64 text-left leading-relaxed">
        {text}
      </TooltipContent>
    </Tooltip>
  );
}

export { Hint, type HintProps };
