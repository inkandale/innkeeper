-- A block rule keeps its matching cards off one target instance.
ALTER TABLE rule DROP CONSTRAINT rule_action_check;
ALTER TABLE rule ADD CONSTRAINT rule_action_check CHECK (action IN ('archive', 'collect', 'push', 'block'));
ALTER TABLE rule DROP CONSTRAINT rule_instance_check;
ALTER TABLE rule ADD CONSTRAINT rule_instance_check CHECK ((action IN ('push', 'block')) = (instance_id IS NOT NULL));
