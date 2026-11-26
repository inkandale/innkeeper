-- A push rule keeps its matching cards on one target instance. No foreign
-- key: rules back up with the library, instances with config, and deleting
-- an instance deletes its push rules.
ALTER TABLE rule ADD COLUMN instance_id bigint;
ALTER TABLE rule DROP CONSTRAINT rule_action_check;
ALTER TABLE rule ADD CONSTRAINT rule_action_check CHECK (action IN ('archive', 'collect', 'push'));
ALTER TABLE rule ADD CONSTRAINT rule_instance_check CHECK ((action = 'push') = (instance_id IS NOT NULL));
CREATE INDEX ON rule (instance_id) WHERE instance_id IS NOT NULL;
